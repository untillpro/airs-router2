module github.com/untillpro/airs-router

go 1.12

require (
	github.com/dgrijalva/jwt-go v3.2.0+incompatible
	github.com/gorilla/mux v1.7.0
	github.com/untillpro/airs-ibus v0.0.0-20200122080602-4e173aa23585
	github.com/untillpro/airs-ibusnats v0.0.0-20200117120140-b80144d71903
	github.com/untillpro/airs-iconfig v0.0.0-20190619130446-a72eff7cb94f
	github.com/untillpro/airs-iconfigcon v0.0.0-20190715123734-41d912ce7c51
	github.com/untillpro/gochips v1.9.0
	github.com/untillpro/godif v0.15.0
	golang.org/x/crypto v0.0.0-20190701094942-4def268fd1a4
	golang.org/x/net v0.0.0-20190404232315-eb5bcb51f2a3
)

replace github.com/untillpro/airs-ibusnats => ../airs-ibusnats

replace github.com/untillpro/airs-ibus => ../airs-ibus
