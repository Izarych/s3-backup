$ErrorActionPreference = 'Stop'

go test -tags production ./...
if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }

go vet -tags production ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

go run ./cmd/build
if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
