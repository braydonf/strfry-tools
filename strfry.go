package strfry

import (
	"os"
	"fmt"
	"sync"
	"encoding/json"

	"github.com/nbd-wtf/go-nostr"
)

const (
	FilterMaxBytes = 65535
	FilterMaxAuthors = 950
	MaxConcurrentReqs = 10
)

type SyncUser struct {
	Direction string `json:"dir"`
	PubKey string `json:"pubkey"`
	Relays []string `json:"relays"`
}

type SyncConfig struct {
	LogLevel string `json:"log-level"`
	StrFryBin string `json:"strfry-bin"`
	Users []*SyncUser `json:"users"`
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

type RouterUser struct {
	Name string `koanf:"name"`
	PubKey string `koanf:"pubkey"`
	Depth int `koanf:"depth"`
	RelayDepth int `koanf:"relay-depth"`
	Direction string `koanf:"dir"`
}

type RouterConfig struct {
	LogLevel string `koanf:"log-level"`
	PluginDown string `koanf:"plugin-down"`
	PluginConfig string `koanf:"plugin-config"`
	RouterConfig string `koanf:"router-config"`
	SyncConfig string `koanf:"sync-config"`
	StrFryBin string `koanf:"sync-strfry"`
	DiscoveryRelays []string `koanf:"discovery-relays"`
	Users []RouterUser `koanf:"users"`
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

type Stream struct {
	Direction string `json:"dir"`
	PluginDown string `json:"pluginDown,omitempty"`
	PluginUp string `json:"pluginUp,omitempty"`
	Relays []string `json:"urls"`
	Filter *Filter `json:"filter,omitempty"`
	relaysMap map[string]bool
	relaysMutex sync.RWMutex
	pluginDown *DownPlugin
}

func NewStream(dir string, pluginPath string) *Stream {
	stream := &Stream{
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

func (g *Stream) hasRelay(relay string) bool {
	if _, ok := g.relaysMap[relay]; ok {
		return true
	} else {
		g.relaysMap[relay] = true
		return false
	}
}

func (g *Stream) SetNewPlugin() {
	g.pluginDown = NewDownPlugin()
}

func (g *Stream) SetPlugin(plugin *DownPlugin) {
	g.pluginDown = plugin
}

func (g *Stream) GetPlugin() *DownPlugin {
	return g.pluginDown
}

func (g *Stream) PluginAppendUniqueAuthor(pubkey string) {
	g.pluginDown.AppendUniqueAuthor(pubkey)
}

func (g *Stream) AppendUniqueRelay(relay string) {
	g.relaysMutex.Lock()
	defer g.relaysMutex.Unlock()
	if !g.hasRelay(relay) {
		g.Relays = append(g.Relays, relay)
	}
}

func (g *Stream) WritePluginConfig(path string) error {
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

type Router struct {
	Streams map[string]*Stream `json:"streams"`
	streamsMutex sync.RWMutex
}

func (g *Router) AddUser(
	cfg *RouterConfig,
	user *RouterUser,
	relays []string,
	contacts []string) error {

	g.streamsMutex.Lock()
	defer g.streamsMutex.Unlock()

	dir := user.Direction

	pluginConf := fmt.Sprintf("%s-pk-%s.json", cfg.PluginConfig, user.PubKey)
	pluginCmd := fmt.Sprintf("%s --conf=%s", cfg.PluginDown, pluginConf)

	streamIndex := 0
	streamName := fmt.Sprintf("pk-%s-%d", user.PubKey, streamIndex)

	if dir == "up" {
		g.Streams[streamName] = NewStream("up", "")
	} else if dir == "down" || dir == "both" {
		g.Streams[streamName] = NewStream(dir, pluginCmd)
	}

	stream := g.Streams[streamName]

	if dir == "down" || dir == "both" {
		stream.Filter.AppendUniqueAuthor(user.PubKey)
		stream.SetNewPlugin()
		stream.PluginAppendUniqueAuthor(user.PubKey)
	}

	for _, relay := range relays {
		stream.AppendUniqueRelay(relay)
	}

	for index, hex := range contacts {
		if dir == "down" || dir == "both" {
			if (index + 2) % FilterMaxAuthors == 0 {
				streamIndex++
				streamName = fmt.Sprintf("pk-%s-%d", user.PubKey, streamIndex)
				pluginDown := stream.GetPlugin()
				g.Streams[streamName] = NewStream(dir, pluginCmd)
				stream = g.Streams[streamName]
				stream.SetPlugin(pluginDown)
			}

			stream.Filter.AppendUniqueAuthor(hex)
			stream.PluginAppendUniqueAuthor(hex)
		}
	}

	// Save the plugin configuration.
	return stream.WritePluginConfig(pluginConf)
}
