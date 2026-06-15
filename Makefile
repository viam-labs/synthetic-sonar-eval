.PHONy: setup


setup:
	@echo "VIAM_AUTH_TOKEN=$(shell viam login print-access-token)" > .env