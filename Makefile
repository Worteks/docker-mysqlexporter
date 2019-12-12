SKIP_SQUASH?=1

.PHONY: build
build:
	SKIP_SQUASH=$(SKIP_SQUASH) hack/build.sh

.PHONY: run
run:
	test -z "$$SQL_HOST" && SQL_HOST=172.17.0.2; \
	docker run \
	    -e DATA_SOURCE_HOST=$$SQL_HOST \
	    -e DATA_SOURCE_USER=root \
	    -e DATA_SOURCE_PASS=secret \
	    -p 9113:9113 wsweet/mysqlexporter

.PHONY: ocbuild
ocbuild: occheck
	oc process -f openshift/imagestream.yaml -p FRONTNAME=wsweet | oc apply -f-
	BRANCH=`git rev-parse --abbrev-ref HEAD`; \
	if test "$$GIT_DEPLOYMENT_TOKEN"; then \
	    oc process -f openshift/build-with-secret.yaml \
		-p "FRONTNAME=wsweet" \
		-p "GIT_DEPLOYMENT_TOKEN=$$GIT_DEPLOYMENT_TOKEN" \
		-p "MYSQL_EXPORTER_REPOSITORY_REF=$$BRANCH" \
		| oc apply -f-; \
	else \
	    oc process -f openshift/build.yaml \
		-p "FRONTNAME=wsweet" \
		-p "MYSQL_EXPORTER_REPOSITORY_REF=$$BRANCH" \
		| oc apply -f-; \
	fi

.PHONY: occheck
occheck:
	oc whoami >/dev/null 2>&1 || exit 42

.PHONY: occlean
occlean: occheck
	oc process -f openshift/run-ephemeral.yaml -p FRONTNAME=wsweet | oc delete -f- || true

.PHONY: ocdemoephemeral
ocdemoephemeral: ocbuild
	oc process -f openshift/run-ephemeral.yaml -p FRONTNAME=wsweet | oc apply -f-

.PHONY: ocdemo
ocdemo: ocdemoephemeral

.PHONY: ocpurge
ocpurge: occlean
	oc process -f openshift/build.yaml -p FRONTNAME=wsweet | oc delete -f- || true
	oc process -f openshift/imagestream.yaml -p FRONTNAME=wsweet | oc delete -f- || true
