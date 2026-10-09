// Package pushx delivers push notifications through Firebase Cloud Messaging
// (FCM HTTP v1) and enqueues delivery work on the Asynq queue.
//
// Design notes (see spec D23):
//   - Push is best-effort: no data lives only in a push. Failures are logged
//     and counted, never returned to the caller's request path.
//   - Sending is asynchronous: Send() enqueues an Asynq task, FCMSend() is the
//     worker-side handler that performs the HTTP calls.
//   - A push service that is disabled (no credentials configured) is a no-op.
//
// The FCM v1 REST API is called directly (rather than through the Firebase
// Admin SDK) so the backend keeps a small dependency surface and the transport
// can be exercised end-to-end against a stub server in tests.
package pushx

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"

	"github.com/sih26234/food-waste/internal/asynqx"
)

var (
	pushSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "push_sent_total",
		Help: "Total push notifications sent.",
	}, []string{"status"})

	pushChunkTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "push_chunk_total",
		Help: "Total FCM batch chunks sent.",
	})

	pushFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "push_failures_total",
		Help: "Total push delivery failures by reason.",
	}, []string{"reason"})
)

const (
	fcmChunkSize = 500

	// fcmScope is the OAuth2 scope required by the FCM v1 API.
	fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

	defaultFCMBaseURL  = "https://fcm.googleapis.com"
	defaultFCMTokenURL = "https://oauth2.googleapis.com/token"
)

// PushService sends push notifications via Firebase FCM v1.
type PushService struct {
	fcm         *fcmClient
	asynqClient *asynq.Client
	disabled    bool
	log         *zap.Logger
	tokenStore  TokenStore
}

// TokenStore abstracts device token persistence.
type TokenStore interface {
	// GetTokens returns FCM device tokens for the given userIDs.
	GetTokens(ctx context.Context, userIDs []string) ([]DeviceToken, error)
	// PruneTokens removes stale/invalid tokens by their token strings.
	PruneTokens(ctx context.Context, tokens []string) error
}

// DeviceToken pairs a user with their FCM device token.
type DeviceToken struct {
	UserID string
	Token  string
}

// ServiceAccount is the subset of a Google service-account JSON key that the
// FCM v1 API needs.
type ServiceAccount struct {
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

// NewPushService constructs a PushService. When disabled is true the service is
// an inert no-op (used when FCM credentials are absent, e.g. local dev/CI).
func NewPushService(
	ctx context.Context,
	credentialsFile string,
	asynqClient *asynq.Client,
	store TokenStore,
	disabled bool,
	log *zap.Logger,
) (*PushService, error) {
	if log == nil {
		log = zap.NewNop()
	}
	ps := &PushService{
		asynqClient: asynqClient,
		disabled:    disabled,
		log:         log,
		tokenStore:  store,
	}

	if disabled {
		log.Info("push notifications disabled (no-op mode)")
		return ps, nil
	}

	if credentialsFile == "" {
		return nil, errors.New("pushx: credentials file required when push is enabled")
	}

	sa, err := LoadServiceAccount(credentialsFile)
	if err != nil {
		return nil, err
	}

	// Allow overriding the FCM endpoints so delivery can be tested against a
	// local stub without touching Google's production API.
	baseURL := envOr("FCM_BASE_URL", defaultFCMBaseURL)
	tokenURL := envOr("FCM_TOKEN_URL", envOr("GOOGLE_TOKEN_URL", firstNonEmpty(sa.TokenURI, defaultFCMTokenURL)))

	client, err := newFCMClient(sa, baseURL, tokenURL, http.DefaultClient)
	if err != nil {
		return nil, err
	}
	ps.fcm = client

	log.Info("push notifications enabled",
		zap.String("project_id", sa.ProjectID),
		zap.String("fcm_base_url", baseURL))
	return ps, nil
}

// LoadServiceAccount reads and parses a service-account JSON key file.
func LoadServiceAccount(path string) (ServiceAccount, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ServiceAccount{}, fmt.Errorf("pushx: read credentials: %w", err)
	}
	var sa ServiceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return ServiceAccount{}, fmt.Errorf("pushx: parse credentials: %w", err)
	}
	if sa.ProjectID == "" || sa.PrivateKey == "" || sa.ClientEmail == "" {
		return ServiceAccount{}, errors.New("pushx: credentials missing project_id, private_key or client_email")
	}
	return sa, nil
}

// Send enqueues an Asynq push task. If disabled, it is a no-op.
func (s *PushService) Send(
	ctx context.Context,
	userIDs []string,
	eventType, title, body string,
	data map[string]string,
) error {
	if s.disabled {
		s.log.Debug("push disabled, skipping", zap.String("event_type", eventType))
		return nil
	}
	if len(userIDs) == 0 {
		return nil
	}

	payload := asynqx.PushPayload{
		UserIDs:   userIDs,
		EventType: eventType,
		Title:     title,
		Body:      body,
		Data:      data,
	}
	task, err := asynqx.NewPushTask(payload)
	if err != nil {
		return err
	}
	if s.asynqClient == nil {
		s.log.Debug("push queue unavailable, dropping notification (best-effort)",
			zap.String("event_type", eventType))
		return nil
	}
	_, err = s.asynqClient.EnqueueContext(ctx, task,
		asynq.MaxRetry(3),
		asynq.Timeout(30*time.Second),
	)
	return err
}

// FCMSend is called by the Asynq worker to deliver messages via FCM.
// It chunks recipients into ≤500 batches, honours 429 Retry-After headers,
// and prunes UNREGISTERED/INVALID_ARGUMENT tokens.
func (s *PushService) FCMSend(ctx context.Context, userIDs []string, title, body string, data map[string]string) error {
	if s.disabled {
		return nil
	}
	if s.fcm == nil {
		return errors.New("pushx: FCM client not initialized")
	}

	tokens, err := s.tokenStore.GetTokens(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("load device tokens: %w", err)
	}
	if len(tokens) == 0 {
		s.log.Debug("no device tokens for users", zap.Strings("user_ids", userIDs))
		return nil
	}

	var invalidTokens []string
	for start := 0; start < len(tokens); start += fcmChunkSize {
		end := min(start+fcmChunkSize, len(tokens))
		chunk := tokens[start:end]
		pushChunkTotal.Inc()

		for _, dt := range chunk {
			err := s.fcm.SendMessage(ctx, dt.Token, title, body, data)
			switch {
			case err == nil:
				pushSentTotal.WithLabelValues("ok").Inc()
			case isRetryable(err):
				wait := retryAfter(err)
				if wait <= 0 {
					wait = time.Second
				}
				s.log.Warn("FCM rate limited, retrying chunk",
					zap.Duration("wait", wait), zap.Error(err))
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
				if retryErr := s.fcm.SendMessage(ctx, dt.Token, title, body, data); retryErr == nil {
					pushSentTotal.WithLabelValues("ok").Inc()
					continue
				}
				pushFailuresTotal.WithLabelValues("rate_limited").Inc()
				pushSentTotal.WithLabelValues("error").Inc()
			case isInvalidToken(err):
				pushFailuresTotal.WithLabelValues("invalid_token").Inc()
				pushSentTotal.WithLabelValues("failed").Inc()
				invalidTokens = append(invalidTokens, dt.Token)
				s.log.Warn("FCM token invalid, pruning",
					zap.String("user_id", dt.UserID), zap.Error(err))
			default:
				pushFailuresTotal.WithLabelValues("error").Inc()
				pushSentTotal.WithLabelValues("error").Inc()
				s.log.Warn("FCM send failed",
					zap.String("user_id", dt.UserID), zap.Error(err))
			}
		}
	}

	if len(invalidTokens) > 0 {
		if err := s.tokenStore.PruneTokens(ctx, invalidTokens); err != nil {
			s.log.Error("failed to prune invalid tokens", zap.Error(err))
		}
	}
	return nil
}

// ─── FCM v1 HTTP client ──────────────────────────────────────────────────────

// fcmClient talks to the FCM HTTP v1 API using a service-account JWT bearer
// token for authorisation.
type fcmClient struct {
	sa       ServiceAccount
	baseURL  string
	tokenURL string
	http     *http.Client
	key      *rsa.PrivateKey

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
	now         func() time.Time
}

// fcmAPIError is a structured error returned by the FCM v1 API.
type fcmAPIError struct {
	StatusCode int
	Status     string // e.g. "UNREGISTERED", "INVALID_ARGUMENT"
	Message    string
	RetryAfter time.Duration
}

func (e *fcmAPIError) Error() string {
	return fmt.Sprintf("fcm: http %d %s: %s", e.StatusCode, e.Status, e.Message)
}

func newFCMClient(sa ServiceAccount, baseURL, tokenURL string, hc *http.Client) (*fcmClient, error) {
	key, err := parsePrivateKey(sa.PrivateKey)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &fcmClient{
		sa:       sa,
		baseURL:  strings.TrimRight(baseURL, "/"),
		tokenURL: tokenURL,
		http:     hc,
		key:      key,
		now:      time.Now,
	}, nil
}

// parsePrivateKey decodes a PEM RSA private key (PKCS#8 or PKCS#1).
func parsePrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("pushx: private_key is not valid PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("pushx: private_key is not an RSA key")
		}
		return rk, nil
	}
	rk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pushx: parse private_key: %w", err)
	}
	return rk, nil
}

// accessTokenFor returns a cached OAuth2 access token, refreshing it when close
// to expiry.
func (c *fcmClient) accessTokenFor(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.accessToken != "" && now.Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	claims := jwt.MapClaims{
		"iss":   c.sa.ClientEmail,
		"scope": fcmScope,
		"aud":   c.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	assertion, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(c.key)
	if err != nil {
		return "", fmt.Errorf("pushx: sign service-account assertion: %w", err)
	}

	form := strings.NewReader("grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=" + assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, form)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("pushx: token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pushx: token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("pushx: invalid token response: %s", strings.TrimSpace(string(body)))
	}

	c.accessToken = tok.AccessToken
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	// Refresh 60s before expiry.
	c.tokenExpiry = now.Add(ttl - time.Minute)
	return c.accessToken, nil
}

// SendMessage delivers one notification to one device token.
func (c *fcmClient) SendMessage(ctx context.Context, token, title, body string, data map[string]string) error {
	accessToken, err := c.accessTokenFor(ctx)
	if err != nil {
		return err
	}

	msg := map[string]any{
		"message": map[string]any{
			"token":        token,
			"notification": map[string]string{"title": title, "body": body},
			"data":         data,
		},
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/projects/%s/messages:send", c.baseURL, c.sa.ProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("pushx: send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	apiErr := &fcmAPIError{
		StatusCode: resp.StatusCode,
		Status:     http.StatusText(resp.StatusCode),
		Message:    strings.TrimSpace(string(respBody)),
		RetryAfter: parseRetryAfterHeader(resp.Header.Get("Retry-After")),
	}
	// FCM returns errors as {"error":{"status":"UNREGISTERED","message":"..."}}.
	var envelope struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err == nil && envelope.Error.Status != "" {
		apiErr.Status = envelope.Error.Status
		if envelope.Error.Message != "" {
			apiErr.Message = envelope.Error.Message
		}
	}
	return apiErr
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// isInvalidToken reports whether the error means the device token is dead.
func isInvalidToken(err error) bool {
	var apiErr *fcmAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Status {
	case "UNREGISTERED", "NOT_FOUND", "INVALID_ARGUMENT", "SENDER_ID_MISMATCH":
		return true
	}
	return false
}

// isRetryable reports whether the request should be retried after a delay.
func isRetryable(err error) bool {
	var apiErr *fcmAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusTooManyRequests ||
		apiErr.StatusCode == http.StatusServiceUnavailable ||
		apiErr.StatusCode >= 500
}

// retryAfter extracts the server-requested wait time from an FCM error.
func retryAfter(err error) time.Duration {
	var apiErr *fcmAPIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}

// parseRetryAfterHeader understands both delay-seconds and HTTP-date forms.
func parseRetryAfterHeader(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
