package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// v0.24.3 (gece testi GT-19): environment variable names are checked at plan time. The platform drops invisible characters
// (zero-width space, BOM, ...) from names and rejects anything else that is not a valid name, so a key like "​APP_MODE"
// used to be saved as "APP_MODE" and Terraform reported "inconsistent result after apply" and tainted the resource.

var (
	_ resource.ResourceWithValidateConfig = (*containerServiceResource)(nil)
	_ resource.ResourceWithValidateConfig = (*containerPipelineResource)(nil)

	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// envKeyProblem: "" = valid, otherwise a short English/Turkish reason.
func envKeyProblem(k string) string {
	for _, r := range k {
		switch {
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF || r == 0x00AD || r == 0x180E || r == 0x00A0:
			return fmt.Sprintf("contains an invisible character U+%04X (often from copy-paste) / görünmez karakter içeriyor U+%04X (kopyala-yapıştır)", r, r)
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			return "contains whitespace / boşluk içeriyor"
		}
	}
	if !envKeyRe.MatchString(k) {
		return "use letters, digits and _ only, not starting with a digit, max 128 (e.g. APP_MODE) / yalnız harf, rakam ve _ (ör. APP_MODE)"
	}
	u := strings.ToUpper(k)
	if strings.HasPrefix(u, "GYRO_") || strings.HasPrefix(u, "KUBERNETES_") {
		return "names starting with GYRO_ or KUBERNETES_ are reserved / GYRO_ ve KUBERNETES_ ile başlayan adlar ayrılmıştır"
	}
	return ""
}

func validateEnvKeys(ctx context.Context, m types.Map, attr string, diags *diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return
	}
	keys := make([]string, 0, len(m.Elements()))
	for k := range m.Elements() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p := envKeyProblem(k); p != "" {
			diags.AddAttributeError(path.Root(attr).AtMapKey(k), "Invalid variable name / Geçersiz değişken adı",
				fmt.Sprintf("%s key %q: %s.", attr, k, p))
		}
	}
}

func (r *containerServiceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var env, sec types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("env"), &env)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secrets"), &sec)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateEnvKeys(ctx, env, "env", &resp.Diagnostics)
	validateEnvKeys(ctx, sec, "secrets", &resp.Diagnostics)
}

func (r *containerPipelineResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	for _, a := range []string{"build_env", "build_args", "secrets"} {
		var m types.Map
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(a), &m)...)
		if resp.Diagnostics.HasError() {
			return
		}
		validateEnvKeys(ctx, m, a, &resp.Diagnostics)
	}
}
