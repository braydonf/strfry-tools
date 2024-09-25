all: config sync router-plugin write-plugin

config:
	go build -o strfry-config ./cmd/strfry-config.go

sync:
	go build -o strfry-sync ./cmd/strfry-sync.go

router-plugin:
	go build -o strfry-router-plugin ./cmd/strfry-router-plugin.go

write-plugin:
	go build -o strfry-write-plugin ./cmd/strfry-write-plugin.go

clean:
	rm strfry-sync
	rm strfry-config
	rm strfry-router-plugin
	rm strfry-write-plugin
	go clean
