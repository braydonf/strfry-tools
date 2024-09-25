all: sync router router-plugin write-plugin

sync:
	go build -o strfry-sync ./cmd/strfry-sync.go

router:
	go build -o strfry-router ./cmd/strfry-router.go

router-plugin:
	go build -o strfry-router-plugin ./cmd/strfry-router-plugin.go

write-plugin:
	go build -o strfry-write-plugin ./cmd/strfry-write-plugin.go

clean:
	rm strfry-sync
	rm strfry-router
	rm strfry-router-plugin
	rm strfry-write-plugin
	go clean
