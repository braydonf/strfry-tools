package strfry

import (
	"os"
	"fmt"
	"sync"
	"time"
	"encoding/json"
	"regexp"
	"bytes"

	"github.com/nbd-wtf/go-nostr"
)

const (
	FilterMaxBytes = 65535
	FilterMaxAuthors = 950
	MaxConcurrentReqs = 10
	MaxConcurrentSyncs = 10
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

	RouterPluginDownBin string `koanf:"router-plugin-down-bin"`
	RouterPluginConfig string `koanf:"router-plugin-down-config"`
	RouterConfigBase string `koanf:"router-config-base"`

	RoutersConfig string `koanf:"routers-config"`

	SyncConfig string `koanf:"sync-config"`
	SyncStatusFile string `koanf:"sync-status-file"`
	SyncStrFryLogBase string `koanf:"sync-strfry-log-base"`
	SyncStrFryConfig string `koanf:"sync-strfry-config"`

	StrFryBin string `koanf:"strfry-bin"`
	StrFryConfig string `koanf:"strfry-config"`

	DiscoveryRelays []string `koanf:"discovery-relays"`
	Users []User `koanf:"users"`
}

type ConcurrentCounter struct {
	mutex sync.Mutex
	count int
}

func (g *ConcurrentCounter) Begin() {
	g.mutex.Lock()
	g.count++
	g.mutex.Unlock()
}

func (g *ConcurrentCounter) Done() {
	g.mutex.Lock()
	g.count--
	g.mutex.Unlock()
}

func (g *ConcurrentCounter) Value() int {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return g.count
}

func (g *ConcurrentCounter) Wait(max int) {
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

type SyncUser struct {
	Direction string `json:"dir"`
	PubKey string `json:"pubkey"`
	Relays []string `json:"relays"`
}

type SyncConfig struct {
	LogLevel string `koanf:"log-level" json:"log-level"`
	StrFryBin string `koanf:"strfry-bin" json:"strfry-bin"`
	StrFryConfig string `koanf:"strfry-config" json:"strfry-config"`
	StrFryLogBase string `koanf:"strfry-log-base" json:"strfry-log-base"`
	StatusFile string `koanf:"status-file" json:"status-file"`
	Users []*SyncUser `koanf:"users" json:"users"`
	usersMap map[string]bool
	usersMutex sync.RWMutex
}

func NewSyncConfig() SyncConfig {
	return SyncConfig{
		Users: make([]*SyncUser, 0),
		usersMap: make(map[string]bool),
	}
}

func (g *SyncConfig) hasUser(user *SyncUser) bool {
	if _, ok := g.usersMap[user.PubKey]; ok {
		return true
	} else {
		g.usersMap[user.PubKey] = true
		return false
	}
}

func (g *SyncConfig) AppendUniqueUser(user *SyncUser) {
	g.usersMutex.Lock()
	defer g.usersMutex.Unlock()
	if !g.hasUser(user) {
		g.Users = append(g.Users, user)
	}
}

type DownPlugin struct {
	AuthorAllow []string `json:"author-allow"`
	authorAllowMap map[string]bool
	authorAllowMutex sync.RWMutex
}

func NewDownPlugin() *DownPlugin {
	return &DownPlugin{
		AuthorAllow: make([]string, 0),
		authorAllowMap: make(map[string]bool),
	}
}

func (g *DownPlugin) hasAuthorAllow(pubkey string) bool {
	if _, ok := g.authorAllowMap[pubkey]; ok {
		return true
	} else {
		g.authorAllowMap[pubkey] = true
		return false
	}
}

func (g *DownPlugin) AppendUniqueAuthor(pubkey string) {
	g.authorAllowMutex.Lock()
	defer g.authorAllowMutex.Unlock()
	if !g.hasAuthorAllow(pubkey) {
		g.AuthorAllow = append(g.AuthorAllow, pubkey)
	}
}

type Filter struct {
	Filter *nostr.Filter
	authorsMap map[string]bool
	authorsMutex sync.RWMutex
}

func NewFilter() *Filter {
	return &Filter{
		Filter: &nostr.Filter{
			Authors: make([]string, 0),
			Limit: 0,
		},
		authorsMap: make(map[string]bool),
	}
}

func (g *Filter) hasAuthor(author string) bool {
	if _, ok := g.authorsMap[author]; ok {
		return true
	} else {
		g.authorsMap[author] = true
		return false
	}
}

func (g *Filter) AppendUniqueAuthor(author string) {
	g.authorsMutex.Lock()
	defer g.authorsMutex.Unlock()
	if !g.hasAuthor(author) {
		g.Filter.Authors = append(g.Filter.Authors, author)
	}
}

func (g *Filter) MarshalJSON() ([]byte, error) {
	return json.Marshal(g.Filter)
}

func (g *Filter) UnmarshalJSON(data []byte) error {
	var filter nostr.Filter
	err := json.Unmarshal(data, &filter)

	if err != nil {
		return err
	}

	g.Filter = &filter

	// TODO go through and add all authors to the map.

	return nil
}

func (g *Filter) AuthorLength() int {
	g.authorsMutex.Lock()
	defer g.authorsMutex.Unlock()

	return len(g.Filter.Authors)
}

type RouterStream struct {
	Direction string `json:"dir"`
	PluginDown string `json:"pluginDown,omitempty"`
	PluginUp string `json:"pluginUp,omitempty"`
	Relays []string `json:"urls"`
	Filter *Filter `json:"filter,omitempty"`
	relaysMap map[string]bool
	relaysMutex sync.RWMutex
}

func NewRouterStream(dir string, pluginPath string) *RouterStream {
	stream := &RouterStream{
		Direction: dir,
		Relays: make([]string, 0),
		Filter: NewFilter(),
		relaysMap: make(map[string]bool),
	}

	if dir == "down" || dir == "both" {
		stream.PluginDown = pluginPath
	}

	return stream
}

func (g *RouterStream) hasRelay(relay string) bool {
	if _, ok := g.relaysMap[relay]; ok {
		return true
	} else {
		g.relaysMap[relay] = true
		return false
	}
}

func (g *RouterStream) AppendUniqueRelay(relay string) {
	g.relaysMutex.Lock()
	defer g.relaysMutex.Unlock()
	if !g.hasRelay(relay) {
		g.Relays = append(g.Relays, relay)
	}
}

type Routers struct {
	Configs map[string]*RouterConfig
	Config *RoutersConfig
	configsMutex sync.RWMutex
	pluginDown *DownPlugin
}

func NewRouters() Routers {
	routers := Routers{Configs: make(map[string]*RouterConfig)}
	routers.SetNewPlugin()
	return routers
}

func (g *Routers) SetNewPlugin() {
	g.pluginDown = NewDownPlugin()
}

func (g *Routers) SetPlugin(plugin *DownPlugin) {
	g.pluginDown = plugin
}

func (g *Routers) GetPlugin() *DownPlugin {
	return g.pluginDown
}

func (g *Routers) PluginAppendUniqueAuthor(pubkey string) {
	g.pluginDown.AppendUniqueAuthor(pubkey)
}

func (g *Routers) WritePluginConfig(path string) error {
	conf, err := json.MarshalIndent(g.pluginDown, "", "  ")

	if err != nil {
		return err
	} else {
		if err := os.WriteFile(path, conf, 0666); err != nil {
			return err
		}
	}

	return nil
}

func (g *Routers) WriteConfig(path string) error {
	conf, err := json.MarshalIndent(g.Config, "", "  ")

	if err != nil {
		return err
	} else {
		if err := os.WriteFile(path, conf, 0666); err != nil {
			return err
		}
	}

	return nil
}

func (g *Routers) AddUser(
	cfg *Config,
	user *User,
	relays []string,
	contacts []string) {

	g.configsMutex.Lock()
	defer g.configsMutex.Unlock()

	if g.Configs[user.PubKey] == nil {
		g.Configs[user.PubKey] = &RouterConfig{
			Streams: make(map[string]*RouterStream),
			filePath: fmt.Sprintf("%s-pk-%s.config", cfg.RouterConfigBase, user.PubKey),
		}
	}

	router := g.Configs[user.PubKey]

	dir := user.Direction

	pluginCmd := fmt.Sprintf("%s --conf=%s", cfg.RouterPluginDownBin, cfg.RouterPluginConfig)

	streamIndex := 0
	streamName := fmt.Sprintf("pk-%s-%d", user.PubKey, streamIndex)

	if dir == "up" {
		router.Streams[streamName] = NewRouterStream("up", "")
	} else if dir == "down" || dir == "both" {
		router.Streams[streamName] = NewRouterStream(dir, pluginCmd)
	}

	stream := router.Streams[streamName]

	if dir == "down" || dir == "both" {
		stream.Filter.AppendUniqueAuthor(user.PubKey)
		g.PluginAppendUniqueAuthor(user.PubKey)
	}

	for _, relay := range relays {
		stream.AppendUniqueRelay(relay)
	}

	for index, hex := range contacts {
		if dir == "down" || dir == "both" {
			if (index + 2) % FilterMaxAuthors == 0 {
				streamIndex++
				streamName = fmt.Sprintf("pk-%s-%d", user.PubKey, streamIndex)
				router.Streams[streamName] = NewRouterStream(dir, pluginCmd)
				stream = router.Streams[streamName]

				for _, relay := range relays {
					stream.AppendUniqueRelay(relay)
				}
			}

			stream.Filter.AppendUniqueAuthor(hex)
			g.PluginAppendUniqueAuthor(hex)
		}
	}
}

type RoutersConfig struct {
	LogLevel string `koanf:"log-level" json:"log-levl"`
	ConfigBase string `koanf:"config-base" json:"config-base"`
	StrFryBin string `koanf:"strfry-bin" json:"strfry-bin"`
	StrFryConfig string `koanf:"strfry-config" json:"strfry-config"`
}

type RouterConfig struct {
	Streams map[string]*RouterStream `json:"streams"`
	filePath string
	streamsMutex sync.RWMutex
}

func (g *RouterConfig) WriteFile() (string, error) {
	conf, err := json.MarshalIndent(g, "", "  ")

	if err != nil {
		return "", err
	} else {
		if err := os.WriteFile(g.filePath, conf, 0644); err != nil {
			return "", err
		}
	}

	return g.filePath, nil
}

var (
	UnsupportedMsgs = []string{
		"ERROR: negentropy error: negentropy query missing elements",
		"ERROR: bad msg: negentropy disabled",
		"ERROR: bad msg: invalid message",
		"bad message type",
		`invalid: \"value\" does not match any of the allowed types`,
		"Command unrecognized",
		"error: bad message",
		"could not parse command",
		"unknown message type NEG-OPEN",
		"ERROR: bad msg: unknown cmd",
	}

	UnexpectedReg = regexp.MustCompile("^(.*)Unexpected message from relay: \\[\"NOTICE\"\\,")
)

func NegentropyUnsupportedLog(log []byte) bool {
	pair := UnexpectedReg.FindIndex(log)
	matched := false
	if len(pair) == 2 {
		for _, s := range UnsupportedMsgs {
			m := bytes.Contains(log[pair[1]:len(log)], []byte(s))
			if m {
				matched = true
				break
			}
		}
	}
	if matched {
		return true
	}
	return false
}
