all: config sync down-plugin

config:
	go build -o strfry-config ./cmd/strfry-config.go

sync:
	go build -o strfry-sync ./cmd/strfry-sync.go

down-plugin:
	go build -o strfry-down-plugin ./cmd/strfry-down-plugin.go

clean:
	rm strfry-sync
	rm strfry-config
	rm strfry-down-plugin
	go clean
