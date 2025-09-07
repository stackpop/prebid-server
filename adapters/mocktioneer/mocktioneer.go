package mocktioneer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v3/adapters"
	"github.com/prebid/prebid-server/v3/config"
	"github.com/prebid/prebid-server/v3/errortypes"
	"github.com/prebid/prebid-server/v3/macros"
	"github.com/prebid/prebid-server/v3/openrtb_ext"
	"github.com/prebid/prebid-server/v3/util/jsonutil"
)

type adapter struct {
	endpoint *template.Template
}

// Builder for the Mocktioneer adapter
func Builder(bidderName openrtb_ext.BidderName, cfg config.Adapter, server config.Server) (adapters.Bidder, error) {
	// Provide a sensible default if endpoint is not configured
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://mocktioneer.edgecompute.app/openrtb2/auction"
	}
	// Use templated endpoint to allow macros if desired; a plain URL is fine too.
	tmpl, err := template.New("endpointTemplate").Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("unable to parse endpoint url template: %v", err)
	}

	return &adapter{endpoint: tmpl}, nil
}

func getHeaders(req *openrtb2.BidRequest) http.Header {
	h := http.Header{}
	h.Add("Content-Type", "application/json;charset=utf-8")
	h.Add("Accept", "application/json")
	h.Add("X-Openrtb-Version", "2.5")
	if req.Device != nil {
		if len(req.Device.UA) > 0 {
			h.Add("User-Agent", req.Device.UA)
		}
		if len(req.Device.IPv6) > 0 {
			h.Add("X-Forwarded-For", req.Device.IPv6)
		}
		if len(req.Device.IP) > 0 {
			h.Add("X-Forwarded-For", req.Device.IP)
		}
	}
	return h
}

func (a *adapter) MakeRequests(ortbReq *openrtb2.BidRequest, reqInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
	// Allow per-imp ext override for endpoint, else use adapter config
	var endpointURL string
	if len(ortbReq.Imp) > 0 {
		if ext, err := parseImpExt(&ortbReq.Imp[0]); err == nil {
			if len(ext.Endpoint) > 0 {
				endpointURL = ext.Endpoint
			}
		}
	}

	// For each imp, pass through the optional `bid` param to upstream as imp.ext.mocktioneer.bid
	type upstreamExtMock struct {
		Bid float64 `json:"bid,omitempty"`
	}
	type upstreamExt struct {
		Mocktioneer *upstreamExtMock `json:"mocktioneer,omitempty"`
	}
	for i := range ortbReq.Imp {
		if ext, err := parseImpExt(&ortbReq.Imp[i]); err == nil && ext != nil {
			if ext.Bid != 0 {
				ue := upstreamExt{Mocktioneer: &upstreamExtMock{Bid: ext.Bid}}
				if raw, mErr := json.Marshal(&ue); mErr == nil {
					ortbReq.Imp[i].Ext = raw
				}
			} else {
				// Clear imp.Ext to avoid passing bidder params upstream otherwise
				ortbReq.Imp[i].Ext = nil
			}
		} else {
			// no ext; clear to be safe
			ortbReq.Imp[i].Ext = nil
		}
	}
	if endpointURL == "" {
		// Resolve templated endpoint with empty params
		url, err := macros.ResolveMacros(a.endpoint, macros.EndpointTemplateParams{})
		if err != nil {
			return nil, []error{err}
		}
		endpointURL = url
	}

	body, err := json.Marshal(ortbReq)
	if err != nil {
		return nil, []error{err}
	}

	req := &adapters.RequestData{
		Method:  http.MethodPost,
		Uri:     endpointURL,
		Body:    body,
		Headers: getHeaders(ortbReq),
		ImpIDs:  openrtb_ext.GetImpIDs(ortbReq.Imp),
	}
	return []*adapters.RequestData{req}, nil
}

func parseImpExt(imp *openrtb2.Imp) (*openrtb_ext.ExtMocktioneer, error) {
	var bidderExt adapters.ExtImpBidder
	if err := jsonutil.Unmarshal(imp.Ext, &bidderExt); err != nil {
		return nil, &errortypes.BadInput{Message: "ext.bidder not provided"}
	}
	var ext openrtb_ext.ExtMocktioneer
	if err := jsonutil.Unmarshal(bidderExt.Bidder, &ext); err != nil {
		return nil, &errortypes.BadInput{Message: "ext.bidder not provided"}
	}
	return &ext, nil
}

func (a *adapter) MakeBids(ortbReq *openrtb2.BidRequest, reqToBidder *adapters.RequestData, respData *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	if respData.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if respData.StatusCode != http.StatusOK {
		return nil, []error{&errortypes.BadServerResponse{Message: fmt.Sprintf("unexpected status: %d", respData.StatusCode)}}
	}

	var bidResp openrtb2.BidResponse
	if err := jsonutil.Unmarshal(respData.Body, &bidResp); err != nil {
		return nil, []error{&errortypes.BadServerResponse{Message: "invalid JSON"}}
	}
	if len(bidResp.SeatBid) == 0 {
		return nil, []error{&errortypes.BadServerResponse{Message: "empty seatbid"}}
	}

	br := adapters.NewBidderResponseWithBidsCapacity(5)
	if bidResp.Cur != "" {
		br.Currency = bidResp.Cur
	}

	sb := bidResp.SeatBid[0]
	for _, b := range sb.Bid {
		br.Bids = append(br.Bids, &adapters.TypedBid{
			Bid:     &b,
			BidType: mediaTypeForImp(b.ImpID, ortbReq.Imp),
		})
	}
	return br, nil
}

func mediaTypeForImp(impID string, imps []openrtb2.Imp) openrtb_ext.BidType {
	// Default banner unless video/native present
	t := openrtb_ext.BidTypeBanner
	for _, imp := range imps {
		if imp.ID == impID {
			if imp.Video != nil {
				t = openrtb_ext.BidTypeVideo
			} else if imp.Native != nil {
				t = openrtb_ext.BidTypeNative
			}
			return t
		}
	}
	return t
}
