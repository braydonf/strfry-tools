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
	RelayDepth int `koanf:"relay-depth"`
	Direction string `koanf:"dir"`
}

type Config struct {
	LogLevel string `koanf:"log-level"`
	PluginDown string `koanf:"plugin-down"`
	PluginConfig string `koanf:"plugin-config"`
	RouterConfig string `koanf:"router-config"`
	DiscoveryRelays []string `koanf:"discovery-relays"`
	Users []User `koanf:"users"`
}

// StryFry write plugin config.
type StrFryDownPlugin struct {
	AuthorAllow []string `json:"author-allow"`
	authorAllowMap map[string]bool
	authorAllowMutex sync.RWMutex
}

func NewStrFryDownPlugin() StrFryDownPlugin {
	return StrFryDownPlugin{
		AuthorAllow: make([]string, 0),
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

func (g *StrFryDownPlugin) AppendUniqueAuthor(pubkey string) {
	g.authorAllowMutex.Lock()
	defer g.authorAllowMutex.Unlock()
	if !g.hasAuthorAllow(pubkey) {
		g.AuthorAllow = append(g.AuthorAllow, pubkey)
	}
}

type StrFryFilter struct {
	Filter *nostr.Filter
	authorsMap map[string]bool
	authorsMutex sync.RWMutex
}

func NewStrFryFilter() *StrFryFilter {
	return &StrFryFilter{
		Filter: &nostr.Filter{
			Authors: make([]string, 0),
			Limit: 0,
		},
		authorsMap: make(map[string]bool),
	}
}

func (g *StrFryFilter) hasAuthor(author string) bool {
	if _, ok := g.authorsMap[author]; ok {
		return true
	} else {
		g.authorsMap[author] = true
		return false
	}
}

func (g *StrFryFilter) AppendUniqueAuthor(author string) {
	g.authorsMutex.Lock()
	defer g.authorsMutex.Unlock()
	if !g.hasAuthor(author) {
		g.Filter.Authors = append(g.Filter.Authors, author)
	}
}

func (g *StrFryFilter) MarshalJSON() ([]byte, error) {
	return json.Marshal(g.Filter)
}

func (g *StrFryFilter) UnmarshalJSON(data []byte) error {
	var filter nostr.Filter
	err := json.Unmarshal(data, &filter)

	if err != nil {
		return err
	}

	g.Filter = &filter

	// TODO go through and add all authors to the map.

	return nil
}

func (g *StrFryFilter) AuthorLength() int {
	g.authorsMutex.Lock()
	defer g.authorsMutex.Unlock()

	return len(g.Filter.Authors)
}

// StrFry router config.
// - https://github.com/hoytech/strfry/blob/master/docs/router.md
// - https://github.com/taocpp/config/blob/main/doc/Writing-Config-Files.md
type StrFryStream struct {
	Direction string `json:"dir"`
	PluginDown string `json:"pluginDown,omitempty"`
	PluginUp string `json:"pluginUp,omitempty"`
	Relays []string `json:"urls"`
	Filter *StrFryFilter `json:"filter,omitempty"`
	relaysMap map[string]bool
	relaysMutex sync.RWMutex
}

func NewStrFryStream(dir string, pluginPath string) *StrFryStream {
	stream := &StrFryStream{
		Direction: dir,
		Relays: make([]string, 0),
		Filter: NewStrFryFilter(),
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

func (g *StrFryStream) AppendUniqueRelay(relay string) {
	g.relaysMutex.Lock()
	defer g.relaysMutex.Unlock()
	if !g.hasRelay(relay) {
		g.Relays = append(g.Relays, relay)
	}
}

type StrFryRouter struct {
	Streams map[string]*StrFryStream `json:"streams"`
	currentUp string
	currentUpIndex int
	currentDown string
	currentDownIndex int
	currentBoth string
	currentBothIndex int
}

func (g *StrFryRouter) PushToStream(user *User, relays []string, contacts []string) {
	if len(g.currentUp) == 0 {
		g.currentUpIndex = 0
		g.currentUp = "depth-0-up-0"
		g.Streams[g.currentUp] = NewStrFryStream("up", "")
	}

	if len(g.currentDown) == 0 {
		g.currentDownIndex = 0
		g.currentDown = "depth-0-down-0"
		g.Streams[g.currentDown] = NewStrFryStream("down", cfg.PluginDown)
	}

	if len(g.currentBoth) == 0 {
		g.currentBothIndex = 0
		g.currentBoth = "depth-0-both-0"
		g.Streams[g.currentBoth] = NewStrFryStream("both", cfg.PluginDown)
	}

	var stream *StrFryStream

	dir := user.Direction

	switch dir {
	case "up":
		stream = g.Streams[g.currentUp]

		if (stream.Filter.AuthorLength() >= FilterMaxAuthors) {
			g.currentUpIndex += 1
			g.currentUp = fmt.Sprintf("depth-%d-up-%d", user.Depth, g.currentUpIndex)
			g.Streams[g.currentUp] = NewStrFryStream("up", "")

			stream = g.Streams[g.currentUp]
		}

		break
	case "down":
		stream = g.Streams[g.currentDown]

		if (stream.Filter.AuthorLength() >= FilterMaxAuthors) {
			g.currentDownIndex += 1
			g.currentDown = fmt.Sprintf("depth-%d-down-%d", user.Depth, g.currentUpIndex)
			g.Streams[g.currentDown] = NewStrFryStream("down", cfg.PluginDown)

			stream = g.Streams[g.currentDown]
		}

		break
	case "both":
		stream = g.Streams[g.currentBoth]

		if (stream.Filter.AuthorLength() >= FilterMaxAuthors) {
			g.currentBothIndex += 1
			g.currentBoth = fmt.Sprintf("depth-%d-both-%d", user.Depth, g.currentUpIndex)
			g.Streams[g.currentBoth] = NewStrFryStream("both", cfg.PluginDown)

			stream = g.Streams[g.currentBoth]
		}

		break
	}

	if dir == "down" || dir == "both" {
		stream.Filter.AppendUniqueAuthor(user.PubKey)
	}

	plugin.AppendUniqueAuthor(user.PubKey)

	for _, relay := range relays {
		stream.AppendUniqueRelay(relay)
	}

	for _, hex := range contacts {
		if dir == "down" || dir == "both" {
			plugin.AppendUniqueAuthor(hex)
		}
	}
}

const (
	FilterMaxBytes = 65535
	FilterMaxAuthors = 950
	MaxConcurrentReqs = 10
)

type ReqsCounter struct {
	mutex sync.Mutex
	count int
}

func (g *ReqsCounter) Begin() {
	g.mutex.Lock()
	g.count++
	g.mutex.Unlock()
}

func (g *ReqsCounter) Done() {
	g.mutex.Lock()
	g.count--
	g.mutex.Unlock()
}

func (g *ReqsCounter) Value() int {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return g.count
}

func (g *ReqsCounter) Wait(max int) {
	if g.Value() < max {
		return
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		for {
			if g.Value() < max {
				break
			}

			time.Sleep(3*time.Second)
		}

		wg.Done()
	}()

	wg.Wait()
}

var (
	knf = koanf.New(".")
	crn = cron.New(cron.WithSeconds())
	cfg Config
	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr})

	router StrFryRouter
	plugin StrFryDownPlugin

	counter ReqsCounter
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func getLatestKind(
	ctx context.Context,
	exited chan struct{},
	pubkey string,
	kind int,
	pool *nostr.SimplePool,
	relays []string) *nostr.Event {

	// TODO handle the case that relays is empty.

	filters := []nostr.Filter{{
		Kinds:   []int{kind},
		Authors: []string{pubkey},
		Limit:   1,
	}}

	counter.Wait(MaxConcurrentReqs)
	counter.Begin()
	defer counter.Done()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	results := pool.SubManyEose(ctx, relays, filters)

	var latest *nostr.Event

	// TODO Get the last result from this request from on disk
	// so that even if there is a network failure the contact
	// would not be removed from the list. If there is a newer
	// version from the relays, then use that one instead.

	outer:
	for {
		select {
		case res := <-results:
			if res.Event == nil {
				break outer
			}

			if latest != nil && res.Event.CreatedAt > latest.CreatedAt {
				latest = res.Event
			} else {
				latest = res.Event
			}
		case <-ctx.Done():
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
	pubkey string,
	pool *nostr.SimplePool,
	relays []string) []string {

	meta := getLatestKind(ctx, exited, pubkey, nostr.KindRelayListMetadata, pool, relays)

	results := make([]string, 0)

	if meta != nil {
		for _, tag := range meta.Tags.GetAll([]string{"r"}) {
			if len(tag) < 2 {
				log.Warn().Msg("unexpected r tag")
				continue
			}

			relay := nostr.NormalizeURL(tag[1])

			if len(relay) == 0 {
				continue
			}

			results = append(results, relay)
		}
	} else {
		log.Warn().Msg("unexpected no relay meta result")
	}

	return results
}

func getUserFollows(
	ctx context.Context,
	exited chan struct{},
	pubkey string,
	pool *nostr.SimplePool,
	relays []string) []string {

	follows := getLatestKind(ctx, exited, pubkey, nostr.KindContactList, pool, relays)
	pubkeys := make([]string, 0)

	if follows != nil {
		ptags := follows.Tags.GetAll([]string{"p"})

		for _, tag := range ptags {
			if len(tag) < 2 {
				log.Warn().Msg("unexpected p tag")
				continue
			}

			hex := tag[1]
			if nostr.IsValidPublicKeyHex(hex) {
				pubkeys = append(pubkeys, hex)
			} else {
				log.Warn().Str("invalid pubkey: %s", hex)
			}
		}
	} else {
		log.Warn().Msg("unexpected no follow list result")
	}

	return pubkeys
}

func getUsersInfo(
	ctx context.Context,
	exited chan struct{},
	users []User,
	pool *nostr.SimplePool,
	relays []string) {

	wg := sync.WaitGroup{}
	wg.Add(len(users))

	for _, user := range users {
		log.Info().Str("user", user.PubKey).Int("depth", user.Depth).Msg("starting...")

		go func() {
			// Gather all of the relays for the user.
			userRelays := make([]string, 0)
			if user.RelayDepth >= 0 {
				userRelays = getUserRelayMeta(ctx, exited, user.PubKey, pool, relays)
			}

			// Gather all of the pubkey contacts for the user.
			var contacts = make([]string, 0)
			if user.Depth >= 0 {
				contacts = getUserFollows(ctx, exited, user.PubKey, pool, relays)
			}

			// Now add this user to the router.
			router.PushToStream(&user, userRelays, contacts)

			log.Info().Str("user", user.PubKey).Msg("...ended")

			// Now keep going for the next depth.
			if user.Depth > 0 {
				nextUsers := make([]User, 0)

				if user.Direction == "down" || user.Direction == "both" {
					for _, hex := range contacts {
						nextUsers = append(nextUsers, User{
							PubKey: hex,
							Depth: user.Depth - 1,
							RelayDepth: user.RelayDepth - 1,
							Direction: user.Direction,
						})
					}

				}

				if len(nextUsers) > 0 {
					wg.Add(1)

					go func() {
						getUsersInfo(ctx, exited, nextUsers, pool, relays)
						wg.Done()
					}()
				}
			}

			wg.Done()
		}()
	}

	wg.Wait()
}

func main() {
	// Setup the router and plugin.
	router.Streams = make(map[string]*StrFryStream)
	plugin = NewStrFryDownPlugin()

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
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		pool := nostr.NewSimplePool(ctx)

		for _, relayAddr := range cfg.DiscoveryRelays {
			relay, err := pool.EnsureRelay(relayAddr)

			if err != nil {
				fmt.Errorf("relay error: %s", err)
				continue
			}

			defer relay.Close()
		}

		getUsersInfo(ctx, exited, cfg.Users, pool, cfg.DiscoveryRelays)

		conf, err := json.MarshalIndent(router, "", "  ")

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			if err := os.WriteFile(cfg.RouterConfig, conf, 0666); err != nil {
				fmt.Errorf("write error: %s", err)
			}
		}

		pluginConf, err := json.MarshalIndent(plugin, "", "  ")

		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			if err := os.WriteFile(cfg.PluginConfig, pluginConf, 0666); err != nil {
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
