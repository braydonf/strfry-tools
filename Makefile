all: writepolicy router

writepolicy:
	go build -o strfry-writepolicy ./cmd/strfry-writepolicy.go

router:
	go build -o strfry-router ./cmd/strfry-router.go

clean:
	rm strfry-router
	rm strfry-writepolicy
	go clean
