// Separate module so the code generator's YAML dependency stays out of the
// SDK's runtime dependency graph. Not part of the parent module's ./... .
module github.com/hduhelp/hduhelp-neo-sdk-go/internal/gen

go 1.24.0

require gopkg.in/yaml.v3 v3.0.1
