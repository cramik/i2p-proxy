package i2pd

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed certificates
var bundledCertificates embed.FS

// installCertificates writes i2pd's bundled reseed/family TLS trust roots
// into dataDir/certificates if not already present. i2pd's Reseeder loads
// its TLS trust roots from <datadir>/certificates/reseed/*.crt (see
// third_party/i2pd/libi2pd/Reseed.cpp, GetCertsDir in FS.cpp) - without
// them, HTTPS reseed to i2pd's hardcoded reseed hosts fails, the router
// never learns any peers, and every Dial times out with no visible error
// (i2pd's own logger writes to a file in dataDir, not anywhere we surface
// today - see i2pd_linux.go's startEmbeddedRouter). Bundling these in the
// binary via go:embed keeps the "no external runtime dependency" property
// the whole embedded-router design is built on.
func installCertificates(dataDir string) error {
	dst := filepath.Join(dataDir, "certificates")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	return fs.WalkDir(bundledCertificates, "certificates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("certificates", path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := bundledCertificates.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}
