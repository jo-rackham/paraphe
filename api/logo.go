package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"

	// The decoders image.DecodeConfig dispatches to. Blank imports: nothing
	// here calls them by name, and without them DecodeConfig answers
	// "unknown format" for every byte sequence — which would refuse every
	// logo rather than accept a bad one, but for the wrong reason.
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// What a campaign may upload as its logo, and what makes it acceptable.
//
// The bytes are served from the media origin, which is NOT the campaign's
// own host: the session cookie is host-only (see Sessions.Issue), so a
// script that somehow ran there would find neither the cookie nor the
// application's DOM. The interface only ever renders a logo inside an
// <img>, where SVG runs in secure static mode — no script, no external
// fetch. This validation is the third layer, not the only one.

const (
	// maxLogoBytes bounds the DECODED image. It is compared with len(), in
	// bytes, deliberately: an image is not text, and the ceilings in auth.go
	// count runes because their messages promise characters.
	// TestTheBodyCeilingHoldsBothEdges checks that its base64 form still
	// fits under maxBodySize.
	maxLogoBytes = 64 << 10
	// maxLogoPixels bounds each side, read from the image HEADER alone —
	// no pixel is ever decoded here, so a 30000×30000 PNG in 60 KiB costs
	// this process nothing. The ceiling is for the browsers that will
	// decode it.
	maxLogoPixels = 2000
	// Named in every refusal: whoever is uploading needs to know what to
	// export instead, not which rule they broke.
	acceptedLogoTypes = "PNG, JPEG, WebP ou SVG"
)

// The four accepted types. `format` is what image.DecodeConfig calls the
// same thing, and an empty one means "not a raster image".
var logoTypes = map[string]struct{ ext, format string }{
	"image/png":     {ext: "png", format: "png"},
	"image/jpeg":    {ext: "jpg", format: "jpeg"},
	"image/webp":    {ext: "webp", format: "webp"},
	"image/svg+xml": {ext: "svg"},
}

// Logo: an upload that passed every check.
//
// No digest field: it is a STEP, not a fact worth keeping twice — the key
// ends in it, and anything that needs one reads it from there.
type Logo struct {
	ContentType string
	Key         string
	Raw         []byte
}

// readLogo validates a data URI and returns what to store, or the refusal to
// send back — its HTTP code and the sentence the volunteer reads. French,
// because this one is shown in the application.
func readLogo(slug, dataURI string) (*Logo, int, string) {
	mime, raw, ok := parseDataURI(dataURI)
	if !ok {
		return nil, http.StatusBadRequest,
			"Image illisible : attendu un data URI en base64."
	}
	kind, known := logoTypes[mime]
	if !known {
		return nil, http.StatusUnsupportedMediaType, fmt.Sprintf(
			"Format refusé (%s). Formats acceptés : %s.", mime, acceptedLogoTypes)
	}
	if len(raw) == 0 {
		return nil, http.StatusBadRequest, "Image vide."
	}
	if len(raw) > maxLogoBytes {
		return nil, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"Le logo pèse %d Ko, la limite est de %d Ko.",
			len(raw)/1024, maxLogoBytes/1024)
	}

	if kind.format == "" {
		if why := refuseSVG(raw); why != "" {
			return nil, http.StatusBadRequest, why
		}
	} else if why := refuseRaster(kind.format, raw); why != "" {
		return nil, http.StatusBadRequest, why
	}

	// The key carries a digest OF THE CONTENT: different bytes are a
	// different key, which is what lets the public URL be cached for ever
	// and makes a replacement impossible to serve stale.
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])[:16]
	return &Logo{
		ContentType: mime,
		Key:         fmt.Sprintf("logos/%s/%s.%s", slug, digest, kind.ext),
		Raw:         raw,
	}, 0, ""
}

// parseDataURI reads `data:<mime>;base64,<payload>` and nothing else. A
// plain URL is refused rather than fetched: fetching one would make the
// server issue a request to an address a caller chose.
func parseDataURI(s string) (string, []byte, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(s), "data:")
	if !found {
		return "", nil, false
	}
	head, payload, found := strings.Cut(rest, ",")
	if !found {
		return "", nil, false
	}
	mime, encoding, found := strings.Cut(head, ";")
	if !found || strings.ToLower(strings.TrimSpace(encoding)) != "base64" {
		return "", nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", nil, false
	}
	return strings.ToLower(strings.TrimSpace(mime)), raw, true
}

// refuseRaster: the BYTES decide what this is, not the type the client
// declared. A .png whose content is a JPEG would otherwise be stored and
// served under the wrong Content-Type, and a browser told `image/png` about
// JPEG bytes shows nothing at all.
func refuseRaster(format string, raw []byte) string {
	cfg, decoded, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "Image illisible : le contenu ne correspond à aucun format " +
			"connu. Formats acceptés : " + acceptedLogoTypes + "."
	}
	if decoded != format {
		return fmt.Sprintf("Le contenu du fichier est du %s, alors qu'il "+
			"s'annonce en %s. Réexportez l'image dans le format annoncé.",
			strings.ToUpper(decoded), strings.ToUpper(format))
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "Image illisible : dimensions nulles."
	}
	if cfg.Width > maxLogoPixels || cfg.Height > maxLogoPixels {
		return fmt.Sprintf("Le logo fait %d × %d pixels, le maximum est de "+
			"%d de côté.", cfg.Width, cfg.Height, maxLogoPixels)
	}
	return ""
}

// Elements that carry or can carry execution. `foreignObject` embeds
// arbitrary HTML, the other three embed a whole document.
var svgForbidden = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true,
	"embed": true, "object": true, "handler": true,
}

// refuseSVG walks the document and refuses the shapes that make an SVG more
// than a drawing. It returns the sentence to show, or "" when the file is
// acceptable.
func refuseSVG(raw []byte) string {
	d := xml.NewDecoder(bytes.NewReader(raw))
	root := false
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "SVG illisible : le fichier n'est pas du XML valide."
		}
		switch t := token.(type) {
		case xml.Directive:
			// A DOCTYPE, hence possibly an internal entity declaration.
			// Go's parser expands none of it — but the BROWSER that renders
			// this file does, and a handful of nested entities is enough to
			// take a tab down. Nothing a logo needs lives in a DOCTYPE.
			return "SVG refusé : il contient une déclaration DOCTYPE, " +
				"inutile pour une image et coûteuse à interpréter. " +
				"Réexportez-le sans."
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !root {
				if name != "svg" {
					return "Ce fichier ne commence pas par un élément <svg>."
				}
				root = true
			}
			if svgForbidden[name] {
				return fmt.Sprintf("SVG refusé : il contient un élément "+
					"<%s>, qui peut exécuter du code. Réexportez l'image "+
					"depuis votre outil de dessin, sans script.", name)
			}
			if why := refuseSVGAttributes(name, t.Attr); why != "" {
				return why
			}
		}
	}
	if !root {
		return "Ce fichier ne contient aucun élément <svg>."
	}
	return ""
}

func refuseSVGAttributes(element string, attrs []xml.Attr) string {
	for _, a := range attrs {
		name := strings.ToLower(a.Name.Local)
		value := strings.ToLower(strings.TrimSpace(a.Value))
		if strings.HasPrefix(name, "on") {
			return fmt.Sprintf("SVG refusé : l'attribut %q sur <%s> est un "+
				"gestionnaire d'événement. Réexportez l'image sans "+
				"interactivité.", a.Name.Local, element)
		}
		if strings.Contains(value, "javascript:") {
			return fmt.Sprintf("SVG refusé : l'attribut %q sur <%s> pointe "+
				"vers du javascript.", a.Name.Local, element)
		}
		// A reference outside the document: the <img> rendering mode would
		// not follow it, but a file opened directly would. A logo refers
		// only to itself.
		if name == "href" && !strings.HasPrefix(value, "#") && value != "" {
			return fmt.Sprintf("SVG refusé : <%s> référence une ressource "+
				"extérieure (%q). Un logo doit être autonome.", element, a.Value)
		}
	}
	return ""
}
