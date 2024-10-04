all: config sync router router-plugin write-plugin

config:
	go build -o strfry-config ./cmd/strfry-config.go

router:
	go build -o strfry-routers ./cmd/strfry-routers.go

sync:
	go build -o strfry-sync ./cmd/strfry-sync.go

router-plugin:
	go build -o strfry-router-plugin ./cmd/strfry-router-plugin.go

write-plugin:
	go build -o strfry-write-plugin ./cmd/strfry-write-plugin.go

clean:
	rm strfry-sync
	rm strfry-config
	rm strfry-router
	rm strfry-router-plugin
	rm strfry-write-plugin
	go clean
