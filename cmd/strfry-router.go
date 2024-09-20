package main

import (
	"fmt"
	"os"
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
	flag "github.com/spf13/pflag"
)

var (
	k = koanf.New(".")
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
		if err := k.Load(file.Provider(c), yaml.Parser()); err != nil {
			log.Fatal().Err(err).Msg("error loading file: %v")
		}
	}

	// Override command over the config file.
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		log.Fatal().Err(err).Msg("error loading config: %v")
	}

	// Get the settings.
	k.Unmarshal("", &cfg)

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

	// Collect the relays for each user.
	pubkey := cfg.AuthorWotUsers[0].PubKey
	relays := cfg.AuthorMetadataRelays

	meta, err := getRelayListMetadata(ctx, pubkey, relays)
	if err != nil {
		fmt.Printf("%s\n", err)
		return
	}

	fmt.Printf("meta: %s\n", meta)

	// Combine all relays.

	// Configure strfry router with the config file for
	// all of the relays. Run this periodically to update
	// the relays.

	// Periodically run and sync for all users using
	// strfry negentropy. Also run this at start as in
	// the case of downtime or a new user.
}
