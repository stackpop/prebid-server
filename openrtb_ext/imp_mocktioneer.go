package openrtb_ext

// ExtMocktioneer defines the contract for impression.ext.bidder for the Mocktioneer adapter.
// All fields are optional for this mock bidder.
type ExtMocktioneer struct {
	// Endpoint allows overriding the bidder endpoint per-request (optional)
	Endpoint string `json:"endpoint,omitempty"`
	// Bid is a passthrough testing parameter (decimal CPM) that mocktioneer will echo back and use as price
	Bid float64 `json:"bid,omitempty"`
}
