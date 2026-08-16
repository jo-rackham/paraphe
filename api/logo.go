package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"regexp"
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

	// The key carries a digest OF THE CONTENT — so a bucket restored from an
	// older copy is detectable rather than silent, and the public URL can be
	// cached for ever — plus EIGHT BYTES that are never the same twice.
	//
	// Those eight bytes are the reason nothing has to be serialised. A key
	// derived from the content alone can COME BACK: upload an image, replace
	// it, upload the identical file again, and the key the deletion is about
	// to remove is the key the pointer now names. Closing that window meant
	// holding a row lock — hence a pool connection — across a round trip to
	// the store, and a store that stopped answering then took every
	// connection the instance had, readiness probe included. Unique, a key
	// cannot come back, so a deletion can never destroy what anybody points
	// at, and no lock has to span the network. The cost is that uploading
	// the same file twice writes two objects instead of sharing one, which
	// nothing depends on: the first is deleted as the pointer moves.
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])[:16]
	unique := make([]byte, 8)
	if _, err := rand.Read(unique); err != nil {
		// crypto/rand does not fail on any platform this runs on, and a
		// key that is not unique is the whole hazard above: refuse rather
		// than fall back to something predictable.
		return nil, http.StatusInternalServerError,
			"Le nom de fichier n'a pas pu être tiré. Réessayez."
	}
	return &Logo{
		ContentType: mime,
		Key: fmt.Sprintf("logos/%s/%s-%s.%s",
			slug, digest, hex.EncodeToString(unique), kind.ext),
		Raw: raw,
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
// arbitrary HTML, the next three embed a whole document, and the SMIL five
// REWRITE an attribute after the document has loaded — `<animate
// attributeName="href" to="…">` sets a link's target to something the
// validator never saw, which is how a `javascript:` URL got past an
// attribute check that only reads what is written in the file. A logo does
// not animate. `animateColor` is deprecated and engines parse it as
// `animate`, so it belongs to the same list rather than to the version of
// the specification that named it.
var svgForbidden = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true,
	"embed": true, "object": true, "handler": true,
	"animate": true, "animatetransform": true, "animatemotion": true,
	"set": true, "animatecolor": true,
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
			// The encoding is named because Go's decoder reads UTF-8 and
			// nothing else: an `encoding="ISO-8859-1"` declaration, or XML
			// 1.1, fails here and "not valid XML" then sends the uploader
			// looking for a syntax error there is none of.
			return "SVG illisible : le fichier n'est pas du XML valide, " +
				"ou il est encodé autrement qu'en UTF-8. Réexportez-le en " +
				"UTF-8."
		}
		switch t := token.(type) {
		case xml.ProcInst:
			// The XML DECLARATION is not one of those, and the exception is
			// not a corner: `<?xml version="1.0" encoding="UTF-8"?>` is the
			// first line Inkscape, Illustrator and Sketch write, so refusing
			// every ProcInst refused nearly every file a campaign would
			// actually upload — and told them to re-export without a line no
			// export dialog mentions. It names an encoding and fetches
			// nothing.
			if strings.EqualFold(t.Target, "xml") {
				continue
			}
			// `<?xml-stylesheet type="text/xsl" href="data:text/xsl;base64,…"?>`
			// is not decoration: opened as a document, the browser fetches
			// that stylesheet and applies it, and an XSLT may emit HTML with
			// a <script> in it. Verified — the file was served as
			// `text/html` on the media origin with the script running. The
			// switch handled StartElement and Directive and let every
			// processing instruction through.
			return fmt.Sprintf("SVG refusé : il contient une instruction de "+
				"traitement <?%s?>, qui peut charger une feuille de style "+
				"exécutable. Réexportez l'image sans.", t.Target)
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

// A URL scheme, as a BROWSER reads it: tabs, newlines and carriage returns
// are stripped from a URL before the scheme is looked at, so `java<TAB>script:`
// is `javascript:` to Chrome and to nobody comparing strings. Every C0
// control goes, which is what the URL specification says to do.
var rxURLNoise = regexp.MustCompile(`[\x00-\x20]`)

// The rasters a drawing tool embeds when it is told to. A PREFIX of
// `data:image/` is not enough to say "this is one": the noise strip above
// removes the space out of `data:image /html,<script>…` and hands the check
// a value that starts with it, and `data:image/x/html,…` starts with it
// outright. The MIME token is named, and it ends where a data URI says it
// ends. `svg+xml` is absent deliberately — an SVG inside an SVG is a
// document this validator never read.
var inlineRaster = regexp.MustCompile(`^data:image/(png|jpeg|jpg|gif|webp)[;,]`)

func refuseSVGAttributes(element string, attrs []xml.Attr) string {
	for _, a := range attrs {
		name := strings.ToLower(a.Name.Local)
		value := rxURLNoise.ReplaceAllString(strings.ToLower(a.Value), "")
		if strings.HasPrefix(name, "on") {
			return fmt.Sprintf("SVG refusé : l'attribut %q sur <%s> est un "+
				"gestionnaire d'événement. Réexportez l'image sans "+
				"interactivité.", a.Name.Local, element)
		}
		// A scheme is what a value BEGINS with. Read as a substring, this
		// refused a `data-note` that merely spelled the word — and prose is
		// where "javascript:" appears without anything following it.
		if strings.HasPrefix(value, "javascript:") {
			return fmt.Sprintf("SVG refusé : l'attribut %q sur <%s> pointe "+
				"vers du javascript.", a.Name.Local, element)
		}
		// A reference outside the document: the <img> rendering mode would
		// not follow it, but a file opened directly would. A logo refers
		// only to itself.
		//
		// An INLINE raster is not outside the document, and refusing it
		// contradicted this very sentence: "Embed image" in Inkscape,
		// Figma and Illustrator all produce `<image href="data:image/…">`,
		// and every one of those exports was rejected as external. Only
		// `data:image/` — `data:text/html,<script>…` in an <a> is exactly
		// what the rest of this function exists to refuse.
		if name == "href" && value != "" &&
			!strings.HasPrefix(value, "#") &&
			!inlineRaster.MatchString(value) {
			return fmt.Sprintf("SVG refusé : <%s> référence une ressource "+
				"extérieure (%q). Un logo doit être autonome.", element, a.Value)
		}
	}
	return ""
}
