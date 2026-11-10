package main

import (
	"testing"

	"github.com/lepinkainen/moviecatalog/internal/identify"
)

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
