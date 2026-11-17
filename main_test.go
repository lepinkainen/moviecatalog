package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/lepinkainen/moviecatalog/internal/identify"
	"github.com/lepinkainen/moviecatalog/internal/webui"
)

func TestEmbeddedFavicon(t *testing.T) {
	ui := webui.FS()
	index, err := fs.ReadFile(ui, "index.html")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("frontend has not been built")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `rel="icon" type="image/svg+xml" sizes="any" href="/favicon.svg"`) {
		t.Error("index does not declare the SVG favicon")
	}
	icon, err := fs.ReadFile(ui, "favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(icon), "<svg ") {
		t.Error("favicon is not an SVG document")
	}
}

// Guards hand updates of the price list: the default model must stay priced.
func TestDefaultModelIsPriced(t *testing.T) {
	price, err := identify.LookupPrice(modelPrices, defaultModel)
	if err != nil {
		t.Fatal(err)
	}
	if price == nil || price.Input <= 0 || price.Output <= 0 {
		t.Fatalf("price of %s = %+v, want input and output prices", defaultModel, price)
	}
}
