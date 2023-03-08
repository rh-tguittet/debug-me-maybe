STATIC_DLV_NAME=dlv
PLUGIN_FOLDER=~/.local/bin
PLUGIN_NAME=kubectl-dmm

.PHONY: build
build: clean dlv
	GO111MODULE=on go build -o $(PLUGIN_NAME) cmd/kubectl_dmm.go

dlv:
	CGO_ENABLED=0 GOBIN=$(shell pwd) go install -v github.com/go-delve/delve/cmd/dlv@latest

install: dlv
	mkdir -p $(PLUGIN_FOLDER)
	cp $(PLUGIN_NAME) $(PLUGIN_FOLDER)/$(PLUGIN_NAME)
	cp $(STATIC_DLV_NAME) $(PLUGIN_FOLDER)

uninstall:
	rm -f $(PLUGIN_FOLDER)/$(PLUGIN_NAME)
	rm -f $(PLUGIN_FOLDER)/$(STATIC_DLV_NAME)

clean:
	rm -f $(PLUGIN_NAME)

clean-dlv:
	rm -f ./dlv
