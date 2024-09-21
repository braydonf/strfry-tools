package main

import (
	"fmt"
	"os"
	goflag "flag"

	"github.com/jiftechnify/strfrui"
	"github.com/jiftechnify/strfrui/sifters"
	"gopkg.in/yaml.v2"
	"github.com/kelseyhightower/envconfig"
	flag "github.com/spf13/pflag"
)

type Config struct {
	AuthorWhitelist []string `yaml:"author-whitelist" split_words:"true"`
}

func readFile(cfg *Config, conf string) {
	f, err := os.Open(conf)
	defer f.Close()

	if err != nil {
		return
	}

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(cfg)

	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
}

var conf string

func init() {
	flag.StringVar(&conf, "conf", "/etc/strfry-writepolicy.yml", "path to configuration yml file")	}

func main() {
	var cfg Config

	flag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	flag.Parse()

	readFile(&cfg, conf)

	err := envconfig.Process("strfry", &cfg)
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}

	strfrui.New(sifters.AuthorList(cfg.AuthorWhitelist, sifters.Allow)).Run()
}
