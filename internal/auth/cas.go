package auth

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// casSuccess is the CAS 2.0 authenticationSuccess subset we rely on: the
// username plus flat string attributes (memberOf, email, ...). The parser
// below walks tokens instead of struct tags, so it is kept for documentation
// of the wire shape only.
type casSuccess struct {
	User  string
	Attrs map[string][]string
}

// validateCASTicket checks a service ticket against a CAS server (protocol
// 2.0, /serviceValidate). It returns the username and the raw attribute
// multiset; mapping to registry users and groups happens in the SSO layer.
func validateCASTicket(ctx context.Context, client *http.Client, casBase, service, ticket string) (string, map[string][]string, error) {
	if ticket == "" {
		return "", nil, fmt.Errorf("auth: cas: missing ticket")
	}
	q := url.Values{}
	q.Set("service", service)
	q.Set("ticket", ticket)
	target := trimURL(casBase) + "/serviceValidate?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("auth: cas: serviceValidate returned %d", resp.StatusCode)
	}
	return parseCASResponse(body)
}

// parseCASResponse interprets a /serviceValidate document. It walks the XML
// token stream matching on local element names only, so responses validate
// regardless of the namespace prefix the CAS server uses.
func parseCASResponse(body []byte) (string, map[string][]string, error) {
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	var user string
	attrs := make(map[string][]string)
	var failureCode, failureMsg string
	inSuccess := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("auth: cas: cannot parse serviceValidate response: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "authenticationFailure":
			for _, a := range se.Attr {
				if a.Name.Local == "code" {
					failureCode = a.Value
				}
			}
			if cd, err := readLeafText(dec); err == nil {
				failureMsg = strings.TrimSpace(cd)
			}
		case "authenticationSuccess":
			inSuccess = true
		case "user":
			if inSuccess {
				if cd, err := readLeafText(dec); err == nil {
					user = strings.TrimSpace(cd)
				}
			}
		case "attributes":
			if inSuccess {
				collectCASAttributes(dec, attrs)
			}
		}
	}
	if failureCode != "" || failureMsg != "" {
		msg := failureMsg
		if msg == "" {
			msg = "ticket validation failed"
		}
		return "", nil, fmt.Errorf("auth: cas: %s (%s)", msg, failureCode)
	}
	if user == "" {
		return "", nil, fmt.Errorf("auth: cas: serviceValidate returned no authenticated user")
	}
	return user, attrs, nil
}

// readLeafText reads the character data of the current element up to its end
// tag, concatenating nested text (attribute values are flat by convention).
func readLeafText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			b.Write([]byte(t))
		}
	}
	return b.String(), nil
}

// collectCASAttributes gathers every leaf under <attributes> as name ->
// values, consuming up to the attributes end tag.
func collectCASAttributes(dec *xml.Decoder, attrs map[string][]string) {
	depth := 1
	var name string
	var b strings.Builder
	flush := func() {
		if name != "" {
			if v := strings.TrimSpace(b.String()); v != "" {
				attrs[name] = append(attrs[name], v)
			}
		}
		name = ""
		b.Reset()
	}
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				flush()
				name = t.Name.Local
			}
		case xml.EndElement:
			if depth == 2 {
				flush()
			}
			depth--
		case xml.CharData:
			if depth >= 2 {
				b.Write([]byte(t))
			}
		}
	}
}
