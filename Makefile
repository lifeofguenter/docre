SHELL := bash

bold := $(shell tput bold)
norm := $(shell tput sgr0)

REPO_NAME ?= $(notdir $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..))/$(shell basename '$(PWD)')


.PHONY: build
build:
	@echo -e "🔨👷 $(bold)Building$(norm) 👷🔨"

	docker build \
		--pull \
		-t '$(REPO_NAME)' \
		.


.PHONY: publish
publish:
ifeq ($(GIT_BRANCH), master)
	@echo -e "🚀🐳 $(bold)Publishing: $(REPO_NAME):latest$(norm) 🐳🚀"
	docker push '$(REPO_NAME)'
endif
