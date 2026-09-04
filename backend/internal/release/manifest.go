package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SubmissionBundle is everything a user needs to submit without fixing
// anything SeekerStudio was responsible for. It does NOT submit: submission
// is wallet-based (publisher keypair + portal API key) and stays entirely in
// the user's hands, via the Publisher Portal or:
//
//	dapp-store --apk-file <apk> --keypair <publisher-keypair.json> \
//	  --whats-new "<notes>"
//
// The portal matches the APK's package name to the existing portal app, so
// the bundle carries the identity the artifact was verified against rather
// than a dApp id.
type SubmissionBundle struct {
	// Generated is RFC3339; everything else below is real project data.
	Generated      string   `json:"generated"`
	ProjectID      int64    `json:"project_id"`
	ProjectName    string   `json:"project_name"`
	AndroidPackage string   `json:"android_package"`
	VersionCode    int      `json:"version_code"`
	VersionName    string   `json:"version_name"`
	SignerSHA256   string   `json:"signer_sha256"`
	APK            string   `json:"apk"`
	WhatsNew       string   `json:"whats_new"`
	Assets         AssetSet `json:"assets"`
	PublishCommand string   `json:"publish_command"`
	CostsNote      string   `json:"costs_note"`
}

type AssetSet struct {
	Icon        string   `json:"icon,omitempty"`
	Banner      string   `json:"banner,omitempty"`
	Screenshots []string `json:"screenshots,omitempty"`
}

// CatalogInput is genuinely user-provided data collected at export time:
// whats-new notes for this release. Identity, versions, and signer come from
// the project and the verified artifact — never from user input, never
// placeholders.
type CatalogInput struct {
	WhatsNew string
}

// costsNote is the honest SOL disclosure: real publishing costs real SOL
// (portal fees + ArDrive storage, ~0.2 SOL typically) plus KYC/KYB and a
// 2-5 business day review. Surfaced before the user leaves SeekerStudio.
const costsNote = "Publishing to the Solana dApp Store costs real SOL: keep ~0.2 SOL in your " +
	"publisher wallet for transaction fees and ArDrive storage. You will also need to complete " +
	"KYC/KYB in the Publisher Portal, and review typically takes 2-5 business days. " +
	"SeekerStudio never touches your publisher wallet — submission happens with your own " +
	"keypair via publish.solanamobile.com or the dapp-store CLI."

// WriteBundle writes dapp-store.json plus a SUBMIT README with the exact
// commands beside the artifact. Every value is real: identity from the
// project row, versions from the built APK's manifest, signer from the
// verified certificate, assets from the validated collection.
func WriteBundle(dir string, bundle SubmissionBundle) (string, error) {
	bundle.Generated = time.Now().UTC().Format(time.RFC3339)
	if bundle.CostsNote == "" {
		bundle.CostsNote = costsNote
	}
	apkBase := filepath.Base(bundle.APK)
	bundle.PublishCommand = fmt.Sprintf(
		"dapp-store --apk-file ./%s --keypair <publisher-keypair.json> --whats-new %q",
		apkBase, bundle.WhatsNew)

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(dir, "dapp-store.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("# Submit this release to the Solana dApp Store\n\n")
	sb.WriteString("SeekerStudio built and signed this APK; submission is yours, with your own wallet.\n\n")
	sb.WriteString("## Option A — Publisher Portal (recommended for a first release)\n\n")
	sb.WriteString("1. Sign up at https://publish.solanamobile.com and complete KYC/KYB.\n")
	sb.WriteString("2. Create the app in the portal if it does not exist yet (the portal matches\n")
	sb.WriteString(fmt.Sprintf("   it by package name `%s`).\n", bundle.AndroidPackage))
	sb.WriteString("3. Upload this APK as a new version and approve the signing requests.\n\n")
	sb.WriteString("## Option B — CLI\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("npm install -g @solana-mobile/dapp-store-cli\n")
	sb.WriteString("export DAPP_STORE_API_KEY=<key from publish.solanamobile.com/dashboard/settings/api-keys>\n")
	sb.WriteString(bundle.PublishCommand + "\n")
	sb.WriteString("```\n\n")
	sb.WriteString("## Costs\n\n" + bundle.CostsNote + "\n")
	readmePath := filepath.Join(dir, "SUBMIT.md")
	if err := os.WriteFile(readmePath, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return manifestPath, nil
}
