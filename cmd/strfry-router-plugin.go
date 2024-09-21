package main

import (
	"fmt"
	"os"
	"log"

	"github.com/jiftechnify/strfrui"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	flag "github.com/spf13/pflag"
)

type PluginConfig struct {
	AuthorAllow []string `json:"author-allow"`
}

var (
	k = koanf.New(".")
	cfg PluginConfig
	authorsAllowed map[string]struct{}
)

func main() {
	f := flag.NewFlagSet("conf", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	f.String("conf", "strfry-wot-plugin.json", "path to .json config file")
	f.Parse(os.Args[1:])

	filepath, err := f.GetString("conf")
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	provider := file.Provider(filepath)

	LoadConfig(provider)

	provider.Watch(func(event interface{}, err error) {
		if err != nil {
			log.Fatalf("watch error: %v", err)
		}

		k = koanf.New(".")
		LoadConfig(provider)
	})

	strfrui.NewWithSifterFunc(AuthorAllowFn()).Run()
}

func LoadConfig(provider *file.File) {
	if err := k.Load(provider, json.Parser()); err != nil {
		log.Fatalf("error loading config: %v", err)
	}

	k.Unmarshal("", &cfg)

	authorsAllowed = make(map[string]struct{})

	for _, pubkey := range cfg.AuthorAllow {
		authorsAllowed[pubkey] = struct{}{}
	}
}

func AuthorAllowFn() strfrui.SifterFunc {
	return func(input *strfrui.Input) (*strfrui.Result, error) {
		_, ok := authorsAllowed[input.Event.PubKey]
		if ok {
			return input.Accept()
		}

		result := &strfrui.Result{
			ID:     input.Event.ID,
			Action: strfrui.ActionReject,
			Msg:    "blocked: event author is not in the whitelist",
		}

		return result, nil
	}
}
