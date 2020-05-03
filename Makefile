SHELL := bash

bold := $(shell tput bold)
norm := $(shell tput sgr0)

# gh-actions shim
ifdef GITHUB_REPOSITORY
	REPO_NAME := $(GITHUB_REPOSITORY)
endif

ifdef GITHUB_REF
	GIT_BRANCH := $(GITHUB_REF:refs/heads/%=%)
endif

REPO_NAME ?= $(notdir $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..))/$(shell basename '$(PWD)')


$(info [REPO_NAME: $(REPO_NAME)])
$(info [GIT_BRANCH: $(GIT_BRANCH)])


.PHONY: all
all:


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
	$(call docker_login)
	@echo -e "🚀🐳 $(bold)Publishing: $(REPO_NAME):latest$(norm) 🐳🚀"
	docker push '$(REPO_NAME)'
endif


define docker_login
	docker login -u lifeofguenter -p '$(DOCKER_PASSWORD)'
endef
