package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"math/rand"
	"time"
	"context"
	"encoding/json"

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

type StrFryStream struct {
	Direction string `json:"dir"`
	PluginDown string `json:"pluginDown,omitempty"`
	PluginUp string `json:"pluginUp,omitempty"`
	Relays []string `json:"urls"`
}

type StrFryRouter struct {
	Streams map[string]StrFryStream `json:"streams"`
}

var (
	knf = koanf.New(".")
	crn = cron.New(cron.WithSeconds())
	cfg wot.Config
	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr})
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func getLatestKind(
	ctx context.Context,
	pubkey string,
	kind int,
	relays []string) *nostr.Event {

	filters := []nostr.Filter{{
		Kinds:   []int{kind},
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

	return latest
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

	updateUser := func() {
		relays := cfg.AuthorMetadataRelays

		var router StrFryRouter

		router.Streams = make(map[string]StrFryStream)

		up := StrFryStream{
			Direction: "up",
			Relays: make([]string, 0, 20),
		}

		down := StrFryStream{
			Direction: "down",
			PluginDown: "/usr/bin/strfry-writepolicy",
			Relays: make([]string, 0, 20),
		}

		both := StrFryStream{
			Direction: "both",
			PluginDown: "/usr/bin/strfry-writepolicy",
			Relays: make([]string, 0, 20),
		}

		for _, user := range cfg.AuthorWotUsers {
			pubkey := user.PubKey

			meta := getLatestKind(ctx, pubkey, nostr.KindRelayListMetadata, relays)

			if meta != nil {
				for _, tag := range meta.Tags.GetAll([]string{"r"}) {
					if len(tag) < 2 {
						log.Warn().Msg("unexpected r tag")
						continue
					}

					if user.Direction == "down" {
						down.Relays = append(down.Relays, tag[1])
					} else if user.Direction == "up" {
						up.Relays = append(up.Relays, tag[1])
					} else if user.Direction == "both" {
						both.Relays = append(both.Relays, tag[1])
					} else {
						log.Warn().Str("unrecognized dir", user.Direction)
					}
				}
			}

			follows := getLatestKind(ctx, pubkey, nostr.KindContactList, relays)

			// Create a config for writepolicy plugin for all of
			// the public keys followed.

			if follows != nil {
				fmt.Printf("pubkey: %s, follows: %s\n", pubkey, follows)
			}
		}

		router.Streams["wotup"] = up
		router.Streams["wotdown"] = down
		router.Streams["wotboth"] = both

		// Make the configuration file for the strfry
		// router. For more information, visit:
		// https://github.com/hoytech/strfry/blob/master/docs/router.md
		// https://github.com/taocpp/config/blob/main/doc/Writing-Config-Files.md
		conf, err := json.MarshalIndent(router, "", "  ")

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			fmt.Println(string(conf))
		}

		// Save this config to a configured location.
		// Save the write policy config.
	}

	updateUser()

	crn.AddFunc("@every 10m", updateUser)
	crn.Start()

	<-done

	crn.Stop()

	// Periodically run and sync for all users using
	// strfry negentropy. Also run this at start as in
	// the case of downtime or a new user.
}
