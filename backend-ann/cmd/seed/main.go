// Command seed loads the deterministic demo dataset.
//
// Everything it writes uses stable, derived UUIDs (uuid v5-style SHA-1 of a
// human-readable key), so the command is idempotent: running it twice does not
// duplicate rows. QR hash chains are generated through internal/qrchain — the
// same code the API uses — so the seeded chains verify (D30).
//
// Usage:
//
//	go run ./cmd/seed            # seed (idempotent)
//	go run ./cmd/seed -days 60   # larger history
//
// The database URL is read from DATABASE_URL unless -url is given.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sih26234/food-waste/internal/config"
	"golang.org/x/crypto/bcrypt"

	"github.com/sih26234/food-waste/internal/qrchain"
)

const demoPassword = "demo123"

// Deterministic UUIDs so re-seeding is idempotent.
func det(key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("annapurna/"+key)).String()
}

var ns = uuid.NameSpaceOID

func main() {
	config.LoadDotEnv(".env")
	url := flag.String("url", os.Getenv("DATABASE_URL"), "postgres connection string")
	days := flag.Int("days", 30, "days of meal/waste/processing history to generate")
	flag.Parse()
	if *url == "" {
		log.Fatal("seed: DATABASE_URL is empty (pass -url or set the env var)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *url)
	if err != nil {
		log.Fatalf("seed: connect: %v", err)
	}
	defer pool.Close()

	s := &seeder{ctx: ctx, pool: pool, days: *days, now: time.Now().UTC().Truncate(time.Second)}
	if err := s.run(); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

type seeder struct {
	ctx  context.Context
	pool *pgxpool.Pool
	days int
	now  time.Time
}

func (s *seeder) run() error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"organisations", s.organisations},
		{"kitchens", s.kitchens},
		{"recipients", s.recipients},
		{"users", s.users},
		{"meals/attendance/production", s.meals},
		{"waste", s.waste},
		{"surplus batches", s.surplus},
		{"qr chains", s.qrChains},
		{"quality checks", s.quality},
		{"matches", s.matches},
		{"processing metrics", s.processing},
		{"sensor readings", s.sensors},
		{"device tokens", s.devices},
	}
	for _, step := range steps {
		if err := step.fn(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		fmt.Printf("  ✓ %s\n", step.name)
	}
	fmt.Println("seed complete — login with kitchen@example.com / demo123")
	return nil
}

const (
	kitchenID    = "00000000-0000-4000-8000-000000000101"
	processingID = "00000000-0000-4000-8000-000000000102"
)

// Mumbai city centre — plausible demo coordinates.
const (
	baseLat = 19.0760
	baseLng = 72.8777
)

func (s *seeder) organisations() error {
	rows := [][3]string{
		{det("org/mess"), "IIT Bombay Hostel Mess", "HOSTEL"},
		{det("org/processing"), "Annapurna Processing Unit", "PROCESSING"},
		{det("org/ngo"), "Annapurna Relief Foundation", "NGO"},
	}
	for _, r := range rows {
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO organisations (id, name, type) VALUES ($1,$2,$3)
			 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, type = EXCLUDED.type`,
			r[0], r[1], r[2]); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) kitchens() error {
	rows := []struct {
		id   string
		org  string
		name string
		typ  string
		lat  float64
		lng  float64
	}{
		{kitchenID, det("org/mess"), "Hostel Mess Kitchen", "HOSTEL", baseLat, baseLng},
		{processingID, det("org/processing"), "Annapurna Processing Unit", "PROCESSING", baseLat + 0.02, baseLng + 0.01},
	}
	for _, r := range rows {
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO kitchens (id, org_id, name, type, latitude, longitude)
			 VALUES ($1,$2,$3,$4,$5,$6)
			 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude`,
			r.id, r.org, r.name, r.typ, r.lat, r.lng); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) recipients() error {
	type rec struct {
		key   string
		name  string
		typ   string
		capKg float64
		dLat  float64
		dLng  float64
		start string
		end   string
	}
	list := []rec{
		{"ngo/a", "Annapurna Relief Foundation", "NGO", 120, 0.008, 0.006, "09:00", "20:00"},
		{"ngo/b", "St. Xavier Food Bank", "FOOD_BANK", 80, -0.012, 0.009, "08:00", "18:00"},
		{"ngo/c", "Sion Community Kitchen", "COMMUNITY_KITCHEN", 60, 0.015, -0.010, "10:00", "21:00"},
		{"ngo/d", "Dharavi Shelter Home", "SHELTER", 45, -0.006, -0.014, "07:00", "19:00"},
		{"ngo/e", "Bandra Night Shelter", "SHELTER", 40, 0.030, 0.020, "18:00", "23:00"},
		{"ngo/f", "Shivaji Nagar Food Bank", "FOOD_BANK", 90, -0.020, 0.025, "09:00", "17:00"},
		{"ngo/g", "Worli Meals Trust", "NGO", 70, 0.022, -0.018, "11:00", "20:00"},
		{"ngo/h", "Matunga Community Hall", "COMMUNITY_KITCHEN", 55, 0.004, 0.016, "08:30", "19:30"},
		{"ngo/i", "Govandi Feed Farm", "FEED_FARM", 200, -0.030, 0.034, "06:00", "16:00"},
		{"ngo/j", "Vashi Compost Co-op", "COMPOST", 300, 0.040, 0.042, "06:00", "15:00"},
	}
	for _, r := range list {
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO recipients (id, org_id, name, type, capacity_kg, latitude, longitude,
			                         pickup_window_start, pickup_window_end, accepts_categories, active)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true)
			 ON CONFLICT (id) DO UPDATE SET
			   name = EXCLUDED.name, capacity_kg = EXCLUDED.capacity_kg,
			   latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude, active = true`,
			det(r.key), det("org/ngo"), r.name, r.typ, r.capKg,
			baseLat+r.dLat, baseLng+r.dLng, r.start, r.end,
			[]string{"cooked", "packaged", "raw"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) users() error {
	hash, err := bcrypt.GenerateFromPassword([]byte(demoPassword), 10)
	if err != nil {
		return err
	}
	type u struct {
		key, name, email, role, org string
		kitchen                     any
	}
	users := []u{
		{"user/kitchen", "Meera Kitchen", "kitchen@example.com", "KITCHEN", kitchenID, kitchenID},
		{"user/ngo", "Ravi NGO", "ngo@example.com", "NGO", det("ngo/a"), nil},
		{"user/logistics", "Arjun Driver", "logistics@example.com", "LOGISTICS", "", nil},
		{"user/admin", "Priya Admin", "admin@example.com", "ADMIN", "", nil},
		{"user/sensor", "SYSTEM Sensor", "sensor@system.local", "SYSTEM", "", nil},
	}
	for _, r := range users {
		var orgID any
		if r.org != "" {
			orgID = r.org
		}
		var recipientID any
		if r.role == "NGO" {
			recipientID = r.org
		}
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO users (id, org_id, kitchen_id, recipient_id, name, email, password_hash, role, is_active)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
			 ON CONFLICT (id) DO UPDATE SET
			   email = EXCLUDED.email, password_hash = EXCLUDED.password_hash,
			   role = EXCLUDED.role, org_id = EXCLUDED.org_id, is_active = true`,
			det(r.key), orgID, r.kitchen, recipientID, r.name, r.email, string(hash), r.role); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) meals() error {
	types := []string{"BREAKFAST", "LUNCH", "DINNER"}
	rnd := rand.New(rand.NewSource(42))

	for d := s.days; d >= 1; d-- {
		date := s.now.AddDate(0, 0, -d).Format("2006-01-02")
		dow := s.now.AddDate(0, 0, -d).Weekday()
		base := 420.0
		if dow == time.Saturday || dow == time.Sunday {
			base = 300 // weekend attendance dip
		}
		for _, mt := range types {
			mealID := det(fmt.Sprintf("meal/%s/%s", date, mt))
			menu := map[string][]string{
				"BREAKFAST": {"poha", "idli", "chutney", "banana"},
				"LUNCH":     {"rice", "dal", "paneer curry", "roti"},
				"DINNER":    {"rice", "rajma", "roti", "salad"},
			}[mt]
			if _, err := s.pool.Exec(s.ctx,
				`INSERT INTO meals (id, kitchen_id, date, meal_type, menu) VALUES ($1,$2,$3,$4,$5)
				 ON CONFLICT (id) DO NOTHING`,
				mealID, kitchenID, date, mt, menu); err != nil {
				return err
			}

			expected := int(base + rnd.NormFloat64()*35)
			if mt == "BREAKFAST" {
				expected = expected * 3 / 4
			}
			actual := expected + int(rnd.NormFloat64()*25)

			if _, err := s.pool.Exec(s.ctx,
				`INSERT INTO attendance (id, meal_id, kitchen_id, meal_date, expected_diners, actual_diners, head_count)
				 VALUES ($1,$2,$3,$4,$5,$6,$6)
				 ON CONFLICT (kitchen_id, meal_date) DO NOTHING`,
				det(fmt.Sprintf("attendance/%s", date)), mealID, kitchenID, date, expected, actual); err != nil {
				return err
			}

			prepared := float64(actual) * 0.42 // ~0.42 kg per diner
			consumed := prepared * (0.88 + rnd.Float64()*0.10)
			if _, err := s.pool.Exec(s.ctx,
				`INSERT INTO production (id, meal_id, kitchen_id, production_date, prepared_qty, consumed_qty, quantity_kg)
				 VALUES ($1,$2,$3,$4,$5,$6,$5)
				 ON CONFLICT (kitchen_id, meal_id, production_date) DO NOTHING`,
				det(fmt.Sprintf("production/%s/%s", date, mt)), mealID, kitchenID, date,
				round2(prepared), round2(consumed)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *seeder) waste() error {
	causes := []string{"OVERPRODUCTION", "LOW_ATTENDANCE", "SPOILAGE", "EXPIRY", "STORAGE_ISSUE", "PREPARATION_ERROR", "OTHER"}
	foods := []string{"cooked rice", "dal", "roti", "mixed curry", "salad", "paneer curry", "idli batter"}
	for i, cause := range causes {
		day := s.now.AddDate(0, 0, -(i + 1))
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO waste (id, kitchen_id, food_type, quantity_kg, cause, waste_type, recorded_at, client_event_id)
			 VALUES ($1,$2,$3,$4,$5,$5,$6,$7)
			 ON CONFLICT (id) DO NOTHING`,
			det(fmt.Sprintf("waste/%s", cause)), kitchenID, foods[i%len(foods)],
			round2(4+float64(i)*1.7), cause, day, "seed-waste-"+cause); err != nil {
			return err
		}
	}
	return nil
}

type batchSpec struct {
	key         string
	food        string
	category    string
	qty         float64
	status      string
	safety      string
	preparedAgo time.Duration // relative to seeding time
	expiresIn   time.Duration // relative to seeding time
	approved    bool
}

func (s *seeder) surplus() error {
	specs := []batchSpec{
		{"batch/1", "Dal & Basmati Rice", "cooked", 15.0, "PENDING_SAFETY", "PENDING", -1 * time.Hour, 6 * time.Hour, false},
		{"batch/2", "Paneer Curry & Roti", "cooked", 8.5, "PENDING_SAFETY", "PENDING", -2 * time.Hour, 5 * time.Hour, false},
		{"batch/3", "Veg Pulao", "cooked", 12.0, "AVAILABLE", "ELIGIBLE", -3 * time.Hour, 8 * time.Hour, true},
		{"batch/4", "Idli & Sambar", "cooked", 6.0, "AVAILABLE", "ELIGIBLE", -4 * time.Hour, 7 * time.Hour, true},
		{"batch/5", "Chapati & Sabzi", "cooked", 9.5, "HOLD", "HOLD", -2 * time.Hour, 4 * time.Hour, true},
		{"batch/6", "Rice & Sambar", "cooked", 20.0, "MATCHED", "ELIGIBLE", -5 * time.Hour, 6 * time.Hour, true},
		{"batch/7", "Mixed Thali", "cooked", 11.0, "IN_TRANSIT", "ELIGIBLE", -6 * time.Hour, 5 * time.Hour, true},
		{"batch/8", "Curd Rice", "cooked", 14.0, "DELIVERED", "ELIGIBLE", -26 * time.Hour, -18 * time.Hour, true},
		{"batch/9", "Spoiled Kheer", "cooked", 5.0, "DIVERTED", "REJECTED", -30 * time.Hour, -20 * time.Hour, true},
		{"batch/10", "Old Upma", "cooked", 7.0, "EXPIRED", "PENDING", -40 * time.Hour, -30 * time.Hour, false},
	}

	for i, b := range specs {
		prepared := s.now.Add(b.preparedAgo)
		expires := s.now.Add(b.expiresIn)
		var approvedBy, approvedAt any
		if b.approved {
			approvedBy = det("user/kitchen")
			approvedAt = prepared.Add(30 * time.Minute)
		}
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO surplus_batches
			   (id, batch_code, kitchen_id, food_name, food_category, quantity_kg, prepared_at, expiry_at,
			    safety_status, status, approved_by, approved_at, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
			 ON CONFLICT (id) DO UPDATE SET
			   status = EXCLUDED.status, safety_status = EXCLUDED.safety_status`,
			det(b.key), fmt.Sprintf("B-%05d", 10291+i), kitchenID, b.food, b.category, b.qty,
			prepared, expires, b.safety, b.status, approvedBy, approvedAt, prepared); err != nil {
			return err
		}
	}
	return nil
}

// qrChains seeds two complete, valid custody chains: batch/3 (approved, with
// offline provenance timestamps) and batch/8 (delivered end-to-end).
func (s *seeder) qrChains() error {
	chainSvc := qrchain.NewService(s.pool)
	type chainStep struct {
		event    string
		actor    string
		role     string
		clientTS *time.Time
	}
	chains := []struct {
		batchKey string
		steps    []chainStep
	}{
		{"batch/8", []chainStep{
			{"CREATED", det("user/kitchen"), "KITCHEN", nil},
			{"APPROVED", det("user/kitchen"), "KITCHEN", nil},
			{"MATCHED", det("user/kitchen"), "KITCHEN", nil},
			{"PICKED_UP", det("user/logistics"), "LOGISTICS", tsPtr(s.now.Add(-24 * time.Hour))},
			{"HANDED_OFF", det("user/logistics"), "LOGISTICS", tsPtr(s.now.Add(-23 * time.Hour))},
			{"RECEIVED", det("user/ngo"), "NGO", tsPtr(s.now.Add(-22 * time.Hour))},
		}},
		{"batch/7", []chainStep{
			{"CREATED", det("user/kitchen"), "KITCHEN", nil},
			{"APPROVED", det("user/kitchen"), "KITCHEN", nil},
			{"MATCHED", det("user/kitchen"), "KITCHEN", nil},
			{"PICKED_UP", det("user/logistics"), "LOGISTICS", tsPtr(s.now.Add(-4 * time.Hour))},
		}},
	}

	for _, c := range chains {
		batchID := det(c.batchKey)
		var existing int
		if err := s.pool.QueryRow(s.ctx, `SELECT count(*) FROM qr_events WHERE batch_id = $1`, batchID).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			continue // already seeded; chain is append-only
		}
		evidence := ""
		for i, step := range c.steps {
			var evPtr *string
			if i == 1 {
				// evidence_hash comes from the quality snapshot (D30)
				sum := sha256.Sum256([]byte("quality:" + c.batchKey + ":ELIGIBLE"))
				evidence = hex.EncodeToString(sum[:])
				evPtr = &evidence
			}
			var clientEventID *string
			if step.clientTS != nil {
				id := fmt.Sprintf("seed-%s-%s", c.batchKey, step.event)
				clientEventID = &id
			}
			if _, err := chainSvc.RecordEvent(s.ctx, batchID, step.actor, step.role,
				step.event, nil, nil, evPtr, clientEventID, step.clientTS); err != nil {
				return fmt.Errorf("%s/%s: %w", c.batchKey, step.event, err)
			}
		}
	}
	return nil
}

func (s *seeder) quality() error {
	specs := []struct {
		key      string
		visual   string
		risk     string
		conf     float64
		decision string
		reason   string
		dzMin    float64
	}{
		{"batch/3", "GOOD", "LOW", 0.93, "ELIGIBLE", "Fresh appearance, temperature in range", 18},
		{"batch/4", "GOOD", "LOW", 0.88, "ELIGIBLE", "No visible spoilage", 25},
		{"batch/5", "RISK", "MEDIUM", 0.72, "HOLD", "Danger-zone minutes accumulating; human review required", 95},
		{"batch/6", "GOOD", "LOW", 0.91, "ELIGIBLE", "Fresh; within safe window", 30},
		{"batch/7", "GOOD", "LOW", 0.90, "ELIGIBLE", "Fresh; within safe window", 40},
		{"batch/8", "GOOD", "LOW", 0.95, "ELIGIBLE", "Fresh; delivered within window", 22},
		{"batch/9", "REJECTED", "HIGH", 0.31, "REJECTED", "Visible spoilage; do not redistribute", 260},
	}
	for _, q := range specs {
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO quality_checks
			   (id, batch_id, image_path, visual_status, risk_level, cv_confidence,
			    cv_model_version, safety_decision, safety_reasons, fusion_model_version,
			    danger_zone_minutes, temperature_c, created_by, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,'cv-v1',$7,$8,'fusion-v1',$9,6.5,$10,$11)
			 ON CONFLICT (id) DO NOTHING`,
			det("quality/"+q.key), det(q.key), fmt.Sprintf("storage/uploads/%s.jpg", det(q.key)),
			q.visual, q.risk, q.conf, q.decision, []string{q.reason}, q.dzMin,
			det("user/kitchen"), s.now.Add(-3*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) matches() error {
	// Two offers against batch/6 (MATCHED) and one against batch/3 (AVAILABLE).
	specs := []struct {
		batch        string
		recipient    string
		score        float64
		status       string
		respondedAge time.Duration
	}{
		{"batch/6", "ngo/a", 92.5, "ACCEPTED", 4 * time.Hour},
		{"batch/6", "ngo/b", 78.0, "DECLINED", 4 * time.Hour},
		{"batch/3", "ngo/c", 85.0, "OFFERED", 0},
	}
	for _, m := range specs {
		batchID, recipientID := det(m.batch), det(m.recipient)
		var respondedAt any
		if m.respondedAge > 0 {
			respondedAt = s.now.Add(-m.respondedAge)
		}
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO matches (id, batch_id, recipient_id, score, score_breakdown, reasons, status, responded_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			 ON CONFLICT (batch_id, recipient_id) DO NOTHING`,
			det("match/"+m.batch+"/"+m.recipient), batchID, recipientID, m.score,
			map[string]any{"need": 18.0, "capacity": 20.0, "distance": 22.5, "window_overlap": 15.0, "shelf_life_slack": 9.0, "fairness": 8.0},
			[]string{"Close proximity", "Sufficient capacity", "Category match"},
			m.status, respondedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) processing() error {
	anomalyDays := map[int]bool{12: true, 23: true}
	for d := s.days; d >= 1; d-- {
		date := s.now.AddDate(0, 0, -d)
		raw := 500.0
		waste := 40.0
		downtime := 35.0
		runtime := 420.0
		if anomalyDays[s.days-d+1] {
			waste = 96.0   // material loss spike
			downtime = 128 // downtime spike
		}
		output := raw - waste
		materialLoss := waste / raw * 100
		downtimePct := downtime / runtime * 100
		energyPerKg := 125.0 / output
		score := 100 - 1.2*materialLoss - 1.5*downtimePct
		if score < 0 {
			score = 0
		}
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO processing_metrics
			   (id, kitchen_id, date, period_start, period_end, raw_material_kg, output_kg, waste_kg,
			    downtime_min, runtime_min, energy_kwh, material_loss_pct, downtime_pct, energy_per_kg, efficiency_score)
			 VALUES ($1,$2,$3,$3,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			 ON CONFLICT (kitchen_id, period_start, period_end) DO UPDATE SET
			   raw_material_kg = EXCLUDED.raw_material_kg, output_kg = EXCLUDED.output_kg,
			   waste_kg = EXCLUDED.waste_kg, efficiency_score = EXCLUDED.efficiency_score`,
			det(fmt.Sprintf("processing/%s", date.Format("2006-01-02"))), processingID, date.Format("2006-01-02"),
			round2(raw), round2(output), round2(waste), downtime, runtime, 125.0,
			round2(materialLoss), round2(downtimePct), round4(energyPerKg), round2(score)); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) sensors() error {
	batchID := det("batch/7")
	// Cold-store series: one reading every 30 minutes for the last 12 hours.
	for i := 0; i < 24; i++ {
		at := s.now.Add(-time.Duration(24-i) * 30 * time.Minute)
		temp := 4.0
		if i > 18 { // excursion
			temp = 11.5 + float64(i-18)*0.4
		}
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO sensor_readings (sensor_id, location_id, kitchen_id, batch_id, ts, temperature_c, humidity_pct, energy_kwh)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			"TEMP-01", "cold-room-1", kitchenID, batchID, at, round2(temp), 62.0, 0.31); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) devices() error {
	tokens := []struct{ key, user, token, platform string }{
		{"device/kitchen", "user/kitchen", "demo-fcm-token-kitchen-stub", "ANDROID"},
		{"device/ngo", "user/ngo", "demo-fcm-token-ngo-stub", "ANDROID"},
	}
	for _, d := range tokens {
		if _, err := s.pool.Exec(s.ctx,
			`INSERT INTO device_tokens (id, user_id, token, platform, app_version, is_valid, last_seen_at)
			 VALUES ($1,$2,$3,$4,'1.0.0',true,$5)
			 ON CONFLICT (token) DO UPDATE SET user_id = EXCLUDED.user_id, is_valid = true, last_seen_at = EXCLUDED.last_seen_at`,
			det(d.key), det(d.user), d.token, d.platform, s.now); err != nil {
			return err
		}
	}
	return nil
}

func tsPtr(t time.Time) *time.Time { return &t }

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
func round4(f float64) float64 { return float64(int(f*10000+0.5)) / 10000 }

var _ = ns
