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
	"sync"

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


// StryFry write plugin config.
type StrFryDownPlugin struct {
	AuthorAllow []string `json:"author-allow"`
	authorAllowMap map[string]bool
	authorAllowMutex sync.RWMutex
}

func NewStrFryDownPlugin() StrFryDownPlugin {
	return StrFryDownPlugin{
		AuthorAllow: make([]string, 0, 1000),
		authorAllowMap: make(map[string]bool),
	}
}

func (g *StrFryDownPlugin) hasAuthorAllow(pubkey string) bool {
	if _, ok := g.authorAllowMap[pubkey]; ok {
		return true
	} else {
		g.authorAllowMap[pubkey] = true
		return false
	}
}

func (g *StrFryDownPlugin) AppendUnique(pubkey string) {
	g.authorAllowMutex.Lock()
	defer g.authorAllowMutex.Unlock()
	if !g.hasAuthorAllow(pubkey) {
		g.AuthorAllow = append(g.AuthorAllow, pubkey)
	}
}

// StrFry router config.
// - https://github.com/hoytech/strfry/blob/master/docs/router.md
// - https://github.com/taocpp/config/blob/main/doc/Writing-Config-Files.md
type StrFryStream struct {
	Direction string `json:"dir"`
	PluginDown string `json:"pluginDown,omitempty"`
	PluginUp string `json:"pluginUp,omitempty"`
	Relays []string `json:"urls"`
	relaysMap map[string]bool
	relaysMutex sync.RWMutex
}

func NewStrFryStream(dir string, pluginPath string) StrFryStream {
	stream := StrFryStream{
		Direction: dir,
		Relays: make([]string, 0, 20),
		relaysMap: make(map[string]bool),
	}

	if dir == "down" || dir == "both" {
		stream.PluginDown = pluginPath
	}

	return stream
}

func (g *StrFryStream) hasRelay(relay string) bool {
	if _, ok := g.relaysMap[relay]; ok {
		return true
	} else {
		g.relaysMap[relay] = true
		return false
	}
}

func (g *StrFryStream) AppendUnique(relay string) {
	g.relaysMutex.Lock()
	defer g.relaysMutex.Unlock()
	if !g.hasRelay(relay) {
		g.Relays = append(g.Relays, relay)
	}
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
	exited chan struct{},
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

			count := 0

			for evt := range sub.Events {
				results <- evt
				count++
				break
			}

			if count == 0 {
				results <- nil
			}
		}()
	}

	var latest *nostr.Event
	var count int = 0

	outer:
	for {
		select {
		case res := <-results:
			if res == nil {
				break outer
			}

			if latest != nil && res.CreatedAt > latest.CreatedAt {
				latest = res
			} else {
				latest = res
			}

			count++

			if count >= len(relays) {
				break outer
			}
		case <-exited:
			cancel()
			break outer
		}
	}

	return latest
}

func getUsersInfo(
	ctx context.Context,
	exited chan struct{},
	users []wot.User,
	up *StrFryStream,
	down *StrFryStream,
	both *StrFryStream,
	plugin *StrFryDownPlugin,
	relays []string) {

	wg := sync.WaitGroup{}
	wg.Add(len(users))

	for _, user := range users {
		go func() {
			pubkey := user.PubKey

			log.Info().Str("user", user.PubKey).Int("depth", user.Depth).Msg("starting...")

			meta := getLatestKind(ctx, exited, pubkey, nostr.KindRelayListMetadata, relays)

			if meta != nil {
				for _, tag := range meta.Tags.GetAll([]string{"r"}) {
					if len(tag) < 2 {
						log.Warn().Msg("unexpected r tag")
						continue
					}

					if user.Direction == "down" {
						down.AppendUnique(tag[1])
					} else if user.Direction == "up" {
						up.AppendUnique(tag[1])
					} else if user.Direction == "both" {
						both.AppendUnique(tag[1])
					} else {
						log.Warn().Str("unrecognized dir", user.Direction)
					}
				}
			}

			follows := getLatestKind(ctx, exited, pubkey, nostr.KindContactList, relays)

			// Create a config for writepolicy plugin for all of
			// the public keys followed.

			if follows != nil {
				ptags := follows.Tags.GetAll([]string{"p"})
				nextUsers := make([]wot.User, 0, len(ptags))
				nextDepth := user.Depth - 1

				for _, tag := range ptags {
					if len(tag) < 2 {
						log.Warn().Msg("unexpected p tag")
						continue
					}

					hex := tag[1]
					if nostr.IsValidPublicKeyHex(hex) {
						if user.Direction == "down" || user.Direction == "both" {
							plugin.AppendUnique(hex)

							if nextDepth > 0 {
								nextUsers = append(nextUsers, wot.User{
									PubKey: hex,
									Depth: user.Depth - 1,
									Direction: user.Direction,
								})
							}

						} else if user.Direction != "up" {
							log.Warn().Str("unrecognized dir", user.Direction)
						}
					} else {
						log.Warn().Str("invalid pubkey: %s", hex)
					}
				}

				if nextDepth > 0 {
					getUsersInfo(ctx, exited, nextUsers, up, down, both, plugin, relays)
				}
			}

			log.Info().Str("user", user.PubKey).Msg("...ended")

			wg.Done()
		}()
	}

	wg.Wait()
}


func main() {
	// Used to keep the process open and watch for
	// system signals to interrupt the process.
	signals := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// This is used to signal to go routines to
	// cancel any contexts.
	exited := make(chan struct{})

	go func() {
		sig := <-signals
		fmt.Printf("Received signal: %s\n", sig)
		close(exited)
		done <- true
	}()

	// Setup flags, load config and set log level.
	f := SetupFlags()
	LoadConfig(f, &cfg)
	SetLogLevel(cfg.LogLevel)

	// Now do Nostr requests and other things.
	ctx := context.Background()

	updateUsers := func() {
		relays := cfg.AuthorMetadataRelays

		var router StrFryRouter
		router.Streams = make(map[string]StrFryStream)

		up := NewStrFryStream("up", "")
		down := NewStrFryStream("down", "/usr/bin/strfry-writepolicy")
		both := NewStrFryStream("both", "/usr/bin/strfry-writepolicy")
		plugin := NewStrFryDownPlugin()

		getUsersInfo(ctx, exited, cfg.AuthorWotUsers, &up, &down, &both, &plugin, relays)

		router.Streams["wotup"] = up
		router.Streams["wotdown"] = down
		router.Streams["wotboth"] = both

		conf, err := json.MarshalIndent(router, "", "  ")

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			fmt.Println(string(conf))
		}


		pluginConf, err := json.MarshalIndent(plugin, "", "  ")

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			fmt.Println(string(pluginConf))
		}

		// Save this config to a configured location.
		// Save the write policy config.
	}

	updateUsers()

	crn.AddFunc("@every 10m", updateUsers)
	crn.Start()

	<-done

	crn.Stop()

	// Periodically run and sync for all users using
	// strfry negentropy. Also run this at start as in
	// the case of downtime or a new user.
}

func SetLogLevel(lvl string) {
	if lvl == "panic" {
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	} else if (lvl == "fatal") {
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	} else if (lvl == "error") {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	} else if (lvl == "warn") {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	} else if (lvl == "info") {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else if (lvl == "debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else if (lvl == "trace") {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func SetupFlags() *flag.FlagSet {
	f := flag.NewFlagSet("conf", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	f.StringSlice("conf", []string{"config.yml"}, "path to one or more .yml config files")
	f.Parse(os.Args[1:])

	return f
}

func LoadConfig(f *flag.FlagSet, cfg *wot.Config) {
	// Load the config files provided in the commandline.
	filepaths, _ := f.GetStringSlice("conf")
	for _, c := range filepaths {
		if err := knf.Load(file.Provider(c), yaml.Parser()); err != nil {
			log.Fatal().Err(err).Msg("error loading file: %v")
		}
	}

	// Override command over the config file.
	if err := knf.Load(posflag.Provider(f, ".", knf), nil); err != nil {
		log.Fatal().Err(err).Msg("error loading config: %v")
	}

	// Get the settings and set the log level.
	knf.Unmarshal("", &cfg)
}
