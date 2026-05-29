SHELL := bash

bold := $(shell tput bold)
norm := $(shell tput sgr0)

# gh-actions shim
ifdef GITHUB_REPOSITORY
	REPO_NAME := $(GITHUB_REPOSITORY)
endif

ifdef GITHUB_REF
ifneq (,$(findstring refs/heads/,$(GITHUB_REF)))
	GIT_BRANCH := $(GITHUB_REF:refs/heads/%=%)
else ifneq (,$(findstring refs/tags/,$(GITHUB_REF)))
	TAG_NAME := $(GITHUB_REF:refs/tags/%=%)
endif
endif

REPO_NAME ?= lifeofguenter/$(shell basename '$(PWD)')


$(info [REPO_NAME: $(REPO_NAME)])
$(info [GIT_BRANCH: $(GIT_BRANCH)])
$(info [TAG_NAME: $(TAG_NAME)])


.PHONY: all
all: test build


.PHONY: test
test:
	go test ./... -covermode=atomic -coverprofile=coverage.out
	go tool cover -func=coverage.out


.PHONY: build
build:
	go build


.PHONY: docker-build
docker-build:
	@echo -e "🔨👷 $(bold)Building$(norm) 👷🔨"

	docker build \
		--pull \
		-t '$(REPO_NAME)' \
		.


.PHONY: docker-publish
docker-publish:
ifeq ($(GIT_BRANCH), main)
	$(call docker_login)
	@echo -e "🚀🐳 $(bold)Publishing: $(REPO_NAME):latest$(norm) 🐳🚀"
	docker push '$(REPO_NAME)'
else ifdef TAG_NAME
	$(call docker_login)
	@echo -e "🚀🐳 $(bold)Publishing: $(REPO_NAME):$(TAG_NAME)$(norm) 🐳🚀"
	docker tag '$(REPO_NAME)' '$(REPO_NAME):$(TAG_NAME)'
	docker push '$(REPO_NAME):$(TAG_NAME)'
endif


define docker_login
	@echo '$(DOCKER_PASSWORD)' | docker login -u lifeofguenter --password-stdin
endef
