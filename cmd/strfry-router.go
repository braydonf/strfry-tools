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

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/nbd-wtf/go-nostr"
	"github.com/robfig/cron/v3"
	flag "github.com/spf13/pflag"
)

type User struct {
	Name string `koanf:"name"`
	PubKey string `koanf:"pubkey"`
	Depth int `koanf:"depth"`
	Direction string `koanf:"dir"`
}

type Config struct {
	LogLevel string `koanf:"log-level"`
	AuthorWhitelist []string `koanf:"author-whitelist"`
	AuthorMetadataRelays []string `koanf:"author-metadata-relays"`
	AuthorWotUsers []User `koanf:"author-wot-users"`
}

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
	Filter string `json:"filter,omitempty"`
	relaysMap map[string]bool
	relaysMutex sync.RWMutex
}

func NewStrFryStream(dir string, filter string, pluginPath string) StrFryStream {
	stream := StrFryStream{
		Direction: dir,
		Relays: make([]string, 0, 100),
		relaysMap: make(map[string]bool),
	}

	if filter != "" {
		stream.Filter = filter
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
	cfg Config
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

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
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

	// TODO Get the last result from this request from on disk
	// so that even if there is a network failure the contact
	// would not be removed from the list. If there is a newer
	// version from the relays, then use that one instead.

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

func getUserRelayMeta(
	ctx context.Context,
	exited chan struct{},
	user *User,
	up *StrFryStream,
	down *StrFryStream,
	both *StrFryStream,
	relays []string) {

	pubkey := user.PubKey
	meta := getLatestKind(ctx, exited, pubkey, nostr.KindRelayListMetadata, relays)

	if meta != nil {
		for _, tag := range meta.Tags.GetAll([]string{"r"}) {
			if len(tag) < 2 {
				log.Warn().Msg("unexpected r tag")
				continue
			}

			relay := tag[1]

			switch user.Direction {
			case "down":
				down.AppendUnique(relay)
				break
			case "up":
				up.AppendUnique(relay)
				break
			case "both":
				both.AppendUnique(relay)
				break;
			default:
				log.Warn().Str("unrecognized dir", user.Direction)
			}
		}
	} else {
		log.Warn().Msg("unexpected no relay meta result")
	}
}

func getUserFollows(
	ctx context.Context,
	exited chan struct{},
	user *User,
	up *StrFryStream,
	down *StrFryStream,
	both *StrFryStream,
	plugin *StrFryDownPlugin,
	relays []string) {

	pubkey := user.PubKey
	nextDepth := user.Depth - 1

	if nextDepth < 0 {
		return
	}

	follows := getLatestKind(ctx, exited, pubkey, nostr.KindContactList, relays)

	nextUsers := make([]User, 0, 1000)

	if follows != nil {
		ptags := follows.Tags.GetAll([]string{"p"})

		for _, tag := range ptags {
			if len(tag) < 2 {
				log.Warn().Msg("unexpected p tag")
				continue
			}

			hex := tag[1]
			if nostr.IsValidPublicKeyHex(hex) {
				if user.Direction == "down" || user.Direction == "both" {
					plugin.AppendUnique(hex)

					if nextDepth >= 0 {
						nextUsers = append(nextUsers, User{
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

		// Now add the filter to the router config.
		filter := nostr.Filter{
			Authors: make([]string, 0, len(plugin.AuthorAllow)),
			Limit:   0,
		}

		for _, pubkey := range plugin.AuthorAllow {
			filter.Authors = append(filter.Authors, pubkey)
		}

		switch user.Direction {
		case "down":
			down.Filter = filter.String()
			break
		case "both":
			both.Filter = filter.String()
			break
		}
	} else {
		log.Warn().Msg("unexpected no follow list result")
	}

	if len(nextUsers) > 0 && nextDepth >= 0 {
		getUsersInfo(ctx, exited, nextUsers, up, down, both, plugin, relays)
	}
}

func getUsersInfo(
	ctx context.Context,
	exited chan struct{},
	users []User,
	up *StrFryStream,
	down *StrFryStream,
	both *StrFryStream,
	plugin *StrFryDownPlugin,
	relays []string) {

	wg := sync.WaitGroup{}
	wg.Add(len(users))

	for _, user := range users {
		go func() {
			log.Info().Str("user", user.PubKey).Int("depth", user.Depth).Msg("starting...")

			getUserRelayMeta(ctx, exited, &user, up, down, both, relays)

			getUserFollows(ctx, exited, &user, up, down, both, plugin, relays)

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

		// TODO Use a configuration option for these plugin paths.

		up := NewStrFryStream("up", "", "")
		down := NewStrFryStream("down", "", "/usr/bin/strfry-writepolicy")
		both := NewStrFryStream("both", "", "/usr/bin/strfry-writepolicy")
		plugin := NewStrFryDownPlugin()

		getUsersInfo(ctx, exited, cfg.AuthorWotUsers, &up, &down, &both, &plugin, relays)

		router.Streams["wotup"] = up
		router.Streams["wotdown"] = down
		router.Streams["wotboth"] = both

		conf, err := json.MarshalIndent(router, "", "  ")

		// TODO Use a configuration option to determine where to output
		// the configuration files.

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			if err := os.WriteFile("strfry-router.config", conf, 0666); err != nil {
				fmt.Errorf("write error: %s", err)
			}
		}


		pluginConf, err := json.MarshalIndent(plugin, "", "  ")

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			if err := os.WriteFile("strfry-router-plugin.json", pluginConf, 0666); err != nil {
				fmt.Errorf("write error: %s", err)
			}

		}
	}

	updateUsers()

	crn.AddFunc("@every 10m", updateUsers)
	crn.Start()

	<-done

	crn.Stop()

	// TODO Periodically run and sync for all users using
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

	f.StringSlice(
		"conf",
		[]string{"router.yml"},
		"path to one or more .yml router config files")

	f.Parse(os.Args[1:])

	return f
}

func LoadConfig(f *flag.FlagSet, cfg *Config) {
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
