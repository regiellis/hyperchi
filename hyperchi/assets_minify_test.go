package hyperchi

import (
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// The production preset sets Assets.Minify; the asset pipeline only minifies
// when its EnableMinify is set, so the app must carry one into the other.
func TestProductionAssetConfigMinifies(t *testing.T) {
	if !assetConfigFrom(config.Production().Assets).EnableMinify {
		t.Fatal("production asset pipeline does not minify")
	}
	if assetConfigFrom(config.Default().Assets).EnableMinify {
		t.Fatal("development asset pipeline minifies")
	}
}
