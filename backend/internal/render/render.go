package render

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"beacon/internal/geoip"
)

type IPResponse struct {
	IP          string
	City        string
	Country     string
	CountryCode string
	ASN         string
}

func FromLookup(ip string, r geoip.Result) IPResponse {
	return IPResponse{
		IP:          ip,
		City:        r.City,
		Country:     r.Country,
		CountryCode: r.CountryCode,
		ASN:         r.ASN,
	}
}

func emptyToNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (resp IPResponse) JSON() ([]byte, error) {
	payload := struct {
		IP          string  `json:"ip"`
		City        *string `json:"city"`
		Country     *string `json:"country"`
		CountryCode *string `json:"country-code"`
		ASN         *string `json:"asn"`
	}{
		IP:          resp.IP,
		City:        emptyToNull(resp.City),
		Country:     emptyToNull(resp.Country),
		CountryCode: emptyToNull(resp.CountryCode),
		ASN:         emptyToNull(resp.ASN),
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (resp IPResponse) PlainText() string {
	parts := []string{resp.IP}
	if resp.City != "" {
		parts = append(parts, resp.City)
	}
	if resp.Country != "" {
		if resp.CountryCode != "" {
			parts = append(parts, "["+resp.CountryCode+"] "+resp.Country)
		} else {
			parts = append(parts, resp.Country)
		}
	}
	return strings.Join(parts, " ") + "\n"
}

func (resp IPResponse) Write(w http.ResponseWriter, accept string) {
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	if strings.Contains(accept, "application/json") {
		body, err := resp.JSON()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"internal error"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(resp.PlainText()))
}
