module github.com/untillpro/airs-router

go 1.15

require (
	github.com/agiledragon/gomonkey/v2 v2.1.0
	github.com/gorilla/mux v1.8.0
	github.com/stretchr/testify v1.6.1
	github.com/untillpro/airs-ibus v0.0.0-20201228131349-e2152a2bf73e
	github.com/untillpro/airs-ibusnats v0.0.0-20201229152740-25439ef62ec9
	github.com/untillpro/godif v0.18.0
	github.com/valyala/bytebufferpool v1.0.0
	golang.org/x/net v0.0.0-20201224014010-6772e930b67b
)

replace github.com/untillpro/airs-ibusnats => ../airs-ibusnats
