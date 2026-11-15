package provider

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// #3462 B7: the provider takes the API key from a gyro CLI profile (file token or OS key store); token / GYRO_TOKEN win.
func TestProfileToken(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(f, []byte(`default_profile = "is"
[profiles.is]
api_url = "https://example.invalid/api/v1/client"
token = "wck_file"
[profiles.kasa]
token_storage = "keyring"
[profiles.bos]
lang = "tr"
`), 0o600)
	t.Setenv("GYRO_CONFIG_FILE", f)
	old := keyringGet
	t.Cleanup(func() { keyringGet = old })
	keyringGet = func(svc, user string) (string, error) {
		if svc == "gyro-cli" && user == "kasa" {
			return "wck_keyring", nil
		}
		return "", errors.New("secret not found in keyring")
	}
	cases := []struct {
		name     string
		explicit bool
		tok, url string
		err      string
	}{
		{"", false, "wck_file", "https://example.invalid/api/v1/client", ""}, // default_profile
		{"kasa", true, "wck_keyring", "", ""},
		{"bos", true, "", "", "has no API key"},
		{"yok", true, "", "", "not found"},
	}
	for _, c := range cases {
		tok, u, _, err := profileToken(c.name, c.explicit)
		if c.err != "" {
			if err == nil || !regexp.MustCompile(c.err).MatchString(err.Error()) {
				t.Fatalf("%q: err %v, want %s", c.name, err, c.err)
			}
			continue
		}
		if err != nil || tok != c.tok || u != c.url {
			t.Fatalf("%q: %q %q %v", c.name, tok, u, err)
		}
	}
	t.Setenv("GYRO_CONFIG_FILE", filepath.Join(dir, "yok.toml"))
	if tok, _, _, err := profileToken("", false); err != nil || tok != "" {
		t.Fatalf("missing file, default profile: %q %v", tok, err)
	}
	if _, _, _, err := profileToken("is", true); err == nil {
		t.Fatal("missing file, named profile: no error")
	}
}

func TestAccMockProviderProfile(t *testing.T) {
	srv := httptest.NewServer(mockapi.New())
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(f, []byte(fmt.Sprintf("[profiles.gyro-proje]\napi_url = %q\ntoken_storage = \"keyring\"\n", srv.URL+"/api/v1/client")), 0o600)
	t.Setenv("GYRO_CONFIG_FILE", f)
	t.Setenv("GYRO_TOKEN", "")
	t.Setenv("GYRO_ENDPOINT", "")
	old := keyringGet
	t.Cleanup(func() { keyringGet = old })
	keyringGet = func(string, string) (string, error) { return "wck_testA", nil }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: "provider \"gyro\" {\n  profile = \"baska\"\n}\ndata \"gyro_locations\" \"all\" {}\n", ExpectError: regexp.MustCompile(`profile "baska" not found`)},
			{Config: "provider \"gyro\" {\n  profile = \"gyro-proje\"\n}\ndata \"gyro_locations\" \"all\" {}\n",
				Check: resource.TestCheckResourceAttr("data.gyro_locations.all", "locations.#", "2")},
		},
	})
}
