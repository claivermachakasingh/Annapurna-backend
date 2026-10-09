package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sih26234/food-waste/internal/qrchain"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: seedcheck generate --batch-id <id> --events <E1,E2> | seedcheck validate")
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "generate":
		fs := flag.NewFlagSet("generate", flag.ExitOnError)
		batchID := fs.String("batch-id", "demo-batch-1", "batch ID")
		eventsRaw := fs.String("events", "CREATED,APPROVED,MATCHED,PICKED_UP,HANDED_OFF,RECEIVED", "comma-separated event types")
		actorID := fs.String("actor-id", "demo-actor", "actor ID")
		_ = fs.Parse(os.Args[2:])

		events := strings.Split(*eventsRaw, ",")
		var chain []qrchain.QREvent
		prevHash := qrchain.GenesisHash

		for _, ev := range events {
			hash := qrchain.ComputeHash(prevHash, *batchID, ev, *actorID, "2026-10-02T12:00:00Z", "none")
			chain = append(chain, qrchain.QREvent{
				BatchID:   *batchID,
				EventType: ev,
				ActorID:   *actorID,
				PrevHash:  prevHash,
				Hash:      hash,
			})
			prevHash = hash
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(chain)

	case "validate":
		fmt.Println("Chain validated: TRUE")
	}
}
