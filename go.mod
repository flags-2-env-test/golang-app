module github.com/flags-2-env-test/golang-app

go 1.22

require github.com/oresoftware/flags-2-env/clients/golang v0.0.0

// The module path is the upstream one; the replace points it at the directory
// .zpkg.toml's [install].dir names, so nothing is fetched from the proxy.
replace github.com/oresoftware/flags-2-env/clients/golang => ./.vendor/.zed/oresoftware/flags-2-env/clients/golang
