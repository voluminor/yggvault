package main

//go:generate bash -c "rm -rf target/* tmp/*"

//go:generate go run github.com/amazing-generators/gometagen/cmd/gometagen@latest generate -source _run/values.yml -hash-source . -hash-exclude .git -hash-exclude .idea -hash-exclude target -hash-exclude tmp -format go -out target/meta_gen.go -pkg target -force
//go:generate go run ./_generate/enums
//go:generate go run ./_generate/errors
//go:generate go run github.com/amazing-generators/goconfgen/cmd/goconfgen@latest -source yml/config -out target/stconf -pkg stconf -formats yaml,json,hjson -with-cli=false -force
//go:generate go run github.com/amazing-generators/godepsgen/cmd/godepsgen@latest -source . -out target/dependencies_gen.go -pkg target -skip-licenses -force
//go:generate go run github.com/amazing-generators/goopenapigen/cmd/goopenapigen@latest generate -source yml/openapi -out target/api -pkg api -force
//go:generate go run github.com/amazing-generators/goopenapigen/cmd/goopenapigen@latest json generate -source yml/openapi -out tmp -force
