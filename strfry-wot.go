package wot

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
