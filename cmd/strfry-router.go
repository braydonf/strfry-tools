package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"math/rand"
	"time"
	"context"

	"github.com/braydonf/strfry-wot"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/nbd-wtf/go-nostr"
	"github.com/robfig/cron/v3"
	flag "github.com/spf13/pflag"
)

var (
	knf = koanf.New(".")
	crn = cron.New(cron.WithSeconds())
	cfg wot.Config
	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr})
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func getRelayListMetadata(ctx context.Context, pubkey string, relays []string) (*nostr.Event, error) {
	filters := []nostr.Filter{{
		Kinds:   []int{nostr.KindRelayListMetadata},
		Authors: []string{pubkey},
		Limit:   1,
	}}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	results := make(chan *nostr.Event)

	for _, relayAddr := range relays {
		go func() {
			relay, err := nostr.RelayConnect(ctx, relayAddr)
			if err != nil {
				fmt.Errorf("%s\n", err)
				results <- nil
				return
			}

			sub, err := relay.Subscribe(ctx, filters)
			if err != nil {
				fmt.Errorf("%s\n", err)
				results <- nil
				return
			}

			defer sub.Unsub()

			for evt := range sub.Events {
				results <- evt
				break
			}
		}()
	}

	var latest *nostr.Event
	var count int = 0

	for res := range results {
		if latest != nil && res.CreatedAt > latest.CreatedAt {
			latest = res
		} else {
			latest = res
		}

		count++

		if count >= len(relays) {
			break
		}
	}

	close(results)

	return latest, nil
}

func main() {
	// Setup signal monitoring and keep the process open.
	signals := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		s := <-signals
		fmt.Println()
		fmt.Println(s)
		done <- true
	}()

	f := flag.NewFlagSet("conf", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	// Path to one or more config files to load into koanf along with some config params.
	f.StringSlice("conf", []string{"config.yml"}, "path to one or more .yml config files")
	f.Parse(os.Args[1:])

	// Load the config files provided in the commandline.
	cFiles, _ := f.GetStringSlice("conf")
	for _, c := range cFiles {
		if err := knf.Load(file.Provider(c), yaml.Parser()); err != nil {
			log.Fatal().Err(err).Msg("error loading file: %v")
		}
	}

	// Override command over the config file.
	if err := knf.Load(posflag.Provider(f, ".", knf), nil); err != nil {
		log.Fatal().Err(err).Msg("error loading config: %v")
	}

	// Get the settings.
	knf.Unmarshal("", &cfg)

	// Set the log level.
	if cfg.LogLevel == "panic" {
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	} else if (cfg.LogLevel == "fatal") {
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	} else if (cfg.LogLevel == "error") {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	} else if (cfg.LogLevel == "warn") {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	} else if (cfg.LogLevel == "info") {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else if (cfg.LogLevel == "debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else if (cfg.LogLevel == "trace") {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	} else {
		log.Warn().Str("loglevel", cfg.LogLevel).Msg("unknown log level, using 'info'")
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Now do Nostr stuff.
	ctx := context.Background()

	updateMeta := func() {
		// Collect the relays for each user.
		relays := cfg.AuthorMetadataRelays

		for _, user := range cfg.AuthorWotUsers {
			pubkey := user.PubKey
			meta, err := getRelayListMetadata(ctx, pubkey, relays)
			if err != nil {
				log.Warn().Err(err).Msg("error getting relay list metadata: %v")
				continue
			}

			fmt.Printf("pubkey: %s, meta: %s\n", pubkey, meta)
		}
	}

	updateMeta()

	crn.AddFunc("@every 45s", updateMeta)
	crn.Start()

	<-done

	// Combine all relays.

	// Configure strfry router with the config file for
	// all of the relays. Run this periodically to update
	// the relays.

	// Periodically run and sync for all users using
	// strfry negentropy. Also run this at start as in
	// the case of downtime or a new user.
}
