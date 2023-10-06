plugin:
	$(MAKE) -C go-ds-s3-plugin all

install-plugin:
	$(MAKE) -C go-ds-s3-plugin install

dist-plugin:
	$(MAKE) -C go-ds-s3-plugin dist

check:
	go vet ./...
	staticcheck --checks all ./...
	misspell -error -locale US .

.PHONY: plugin install-plugin check
