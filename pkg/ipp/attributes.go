package ipp

import (
	"github.com/OpenPrinting/goipp"
	"github.com/sschueller/brother-ql570-go/pkg/ql"
)

// Attributes of the printer shared across all operations. All static
// attribute assembly lives in this file so corrections are localized.

// operationAttrs returns the response operation attributes echoing the
// request's charset and language plus a status message.
func operationAttrs(req *goipp.Message, statusMsg string) goipp.Attributes {
	charset := "utf-8"
	language := "en-us"
	if req != nil {
		if a := findAttr(req.Operation, "attributes-charset"); a != nil {
			if s, ok := attrValueString(a); ok && s != "" {
				charset = s
			}
		}
		if a := findAttr(req.Operation, "attributes-natural-language"); a != nil {
			if s, ok := attrValueString(a); ok && s != "" {
				language = s
			}
		}
	}
	attrs := goipp.Attributes{
		goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String(charset)),
		goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String(language)),
	}
	if statusMsg != "" {
		attrs = append(attrs, goipp.MakeAttribute("status-message", goipp.TagText, goipp.String(statusMsg)))
	}
	return attrs
}

// printerAttributes returns the complete set of printer description
// attributes. The dynamic state (printer-state etc.) is computed from the
// given printer status: st is nil when the printer is unavailable.
func printerAttributes(s *Server, req *goipp.Message, st *printerState) goipp.Attributes {
	uuid := s.uuid
	state := 3 // idle
	reasons := goipp.Attribute{Name: "printer-state-reasons"}
	message := ""
	accepting := true
	if st != nil {
		state = st.state
		message = st.message
		accepting = st.accepting
		reasons.Values.Add(goipp.TagKeyword, goipp.String(st.reason))
	} else {
		state = 5 // stopped
		message = "printer not connected"
		reasons.Values.Add(goipp.TagKeyword, goipp.String("printer-unreachable"))
	}

	attrs := goipp.Attributes{
		goipp.MakeAttr("ipp-versions-supported", goipp.TagKeyword, goipp.String("1.1"), goipp.String("2.0")),
		goipp.MakeAttr("operations-supported", goipp.TagEnum,
			goipp.Integer(goipp.OpPrintJob),
			goipp.Integer(goipp.OpValidateJob),
			goipp.Integer(goipp.OpCreateJob),
			goipp.Integer(goipp.OpSendDocument),
			goipp.Integer(goipp.OpCancelJob),
			goipp.Integer(goipp.OpGetJobAttributes),
			goipp.Integer(goipp.OpGetJobs),
			goipp.Integer(goipp.OpGetPrinterAttributes),
			goipp.Integer(goipp.OpIdentifyPrinter),
		),
		goipp.MakeAttr("document-format-supported", goipp.TagMimeType,
			goipp.String(FormatPWGRaster), goipp.String(FormatPDF)),
		goipp.MakeAttr("pwg-raster-document-type-supported", goipp.TagKeyword,
			goipp.String("srgb_8"), goipp.String("sgray_8")),
		goipp.MakeAttr("pwg-raster-document-resolution-supported", goipp.TagResolution,
			goipp.Resolution{Xres: RequiredDPI, Yres: RequiredDPI, Units: goipp.UnitsDpi}),
		goipp.MakeAttribute("printer-resolution-supported", goipp.TagResolution,
			goipp.Resolution{Xres: RequiredDPI, Yres: RequiredDPI, Units: goipp.UnitsDpi}),
		goipp.MakeAttribute("printer-resolution-default", goipp.TagResolution,
			goipp.Resolution{Xres: RequiredDPI, Yres: RequiredDPI, Units: goipp.UnitsDpi}),
		goipp.MakeAttribute("color-supported", goipp.TagBoolean, goipp.Boolean(false)),
		goipp.MakeAttribute("copies-supported", goipp.TagRange, goipp.Range{Lower: 1, Upper: 255}),
		goipp.MakeAttribute("copies-default", goipp.TagInteger, goipp.Integer(1)),
		goipp.MakeAttribute("sides-supported", goipp.TagKeyword, goipp.String("one-sided")),
		goipp.MakeAttribute("sides-default", goipp.TagKeyword, goipp.String("one-sided")),
		goipp.MakeAttribute("printer-uuid", goipp.TagURI, goipp.String(uuid)),
		goipp.MakeAttribute("charset-configured", goipp.TagCharset, goipp.String("utf-8")),
		goipp.MakeAttribute("natural-language-configured", goipp.TagLanguage, goipp.String("en-us")),
		goipp.MakeAttribute("uri-security-supported", goipp.TagKeyword, goipp.String("none")),
		goipp.MakeAttribute("uri-authentication-supported", goipp.TagKeyword, goipp.String("none")),
		goipp.MakeAttribute("printer-name", goipp.TagName, goipp.String(s.name)),
		goipp.MakeAttribute("printer-dns-sd-name", goipp.TagName, goipp.String(s.name)),
		goipp.MakeAttribute("printer-info", goipp.TagText, goipp.String(s.name)),
		goipp.MakeAttribute("printer-make-and-model", goipp.TagText, goipp.String("Brother QL-570")),
		goipp.MakeAttribute("printer-device-id", goipp.TagText,
			goipp.String("MFG:Brother;MDL:QL-570;CMD:PWGRaster,PDF;CLS:PRINTER;DES:Brother QL-570;")),
		goipp.MakeAttribute("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(accepting)),
		goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(state)),
		reasons,
		goipp.MakeAttribute("printer-state-message", goipp.TagText, goipp.String(message)),
		goipp.MakeAttribute("ipp-features-supported", goipp.TagKeyword, goipp.String("ipp-everywhere")),
		goipp.MakeAttribute("pdl-override-supported", goipp.TagKeyword, goipp.String("attempted")),
		goipp.MakeAttribute("queued-job-count", goipp.TagInteger, goipp.Integer(s.store.Count())),
		goipp.MakeAttribute("print-color-mode-supported", goipp.TagKeyword, goipp.String("monochrome")),
		goipp.MakeAttribute("print-color-mode-default", goipp.TagKeyword, goipp.String("monochrome")),
		goipp.MakeAttribute("print-quality-supported", goipp.TagEnum, goipp.Integer(4)), // normal
		goipp.MakeAttr("print-scaling-supported", goipp.TagKeyword,
			goipp.String("auto"), goipp.String("auto-fit"), goipp.String("fit"), goipp.String("fill"), goipp.String("none")),
		goipp.MakeAttribute("print-scaling-default", goipp.TagKeyword, goipp.String("auto-fit")),
		goipp.MakeAttribute("job-pages-per-set-supported", goipp.TagBoolean, goipp.Boolean(false)),
		goipp.MakeAttribute("pwg-raster-document-sheet-back", goipp.TagKeyword, goipp.String("normal")),
		goipp.MakeAttr("media-type-supported", goipp.TagKeyword, goipp.String("labels")),
		goipp.MakeAttribute("media-left-margin-supported", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-right-margin-supported", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-top-margin-supported", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("media-bottom-margin-supported", goipp.TagInteger, goipp.Integer(0)),
		MediaDefaultKeyword(),
		MediaColDefault(),
		MediaColDatabase(),
		MediaSupported(),
	}
	// media-ready / media-col-ready always carry the standard sizes (they
	// map onto whatever is loaded), plus the options matching the loaded
	// media when known. Android falls back to its built-in defaults (A4,
	// Letter, ...) when these are empty, which would let users pick sizes
	// the QL-570 cannot print.
	var readyStatus *ql.Status
	if st != nil {
		readyStatus = st.status
	}
	attrs = append(attrs, MediaColReady(readyStatus), MediaReadyKeywords(readyStatus))
	if uri := attrString(req.Operation, "printer-uri"); uri != "" {
		attrs = append(attrs, goipp.MakeAttribute("printer-uri-supported", goipp.TagURI, goipp.String(uri)))
	}
	return attrs
}

// jobAttributes returns the Job group attributes describing one stored
// job, as used by Get-Jobs and Get-Job-Attributes responses.
func jobAttributes(job *StoredJob, printerURI string) goipp.Attributes {
	state, msg, created, done := job.snapshot()
	attrs := goipp.Attributes{
		goipp.MakeAttribute("job-id", goipp.TagInteger, goipp.Integer(job.ID)),
		goipp.MakeAttribute("job-uri", goipp.TagURI, goipp.String(job.URI)),
		goipp.MakeAttribute("job-printer-uri", goipp.TagURI, goipp.String(printerURI)),
		goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(jobStateEnum(state))),
		goipp.MakeAttribute("job-name", goipp.TagName, goipp.String(job.Name)),
		goipp.MakeAttribute("job-originating-user-name", goipp.TagName, goipp.String(job.User)),
		goipp.MakeAttribute("time-at-creation", goipp.TagInteger, goipp.Integer(created.Unix())),
		goipp.MakeAttribute("job-media-sheets-completed", goipp.TagInteger, goipp.Integer(0)),
		goipp.MakeAttribute("job-impressions-completed", goipp.TagInteger, goipp.Integer(0)),
	}
	reasons := goipp.Attribute{Name: "job-state-reasons"}
	switch state {
	case StatePending, StateProcessing:
		if job.Canceled() {
			reasons.Values.Add(goipp.TagKeyword, goipp.String("processing-to-stop-point"))
		} else {
			reasons.Values.Add(goipp.TagKeyword, goipp.String("none"))
		}
	case StateCanceled:
		reasons.Values.Add(goipp.TagKeyword, goipp.String("job-canceled-by-user"))
	case StateAborted:
		reasons.Values.Add(goipp.TagKeyword, goipp.String("job-completed-with-errors"))
	default:
		reasons.Values.Add(goipp.TagKeyword, goipp.String("none"))
	}
	attrs = append(attrs, reasons)
	if msg != "" {
		attrs = append(attrs, goipp.MakeAttribute("job-state-message", goipp.TagText, goipp.String(msg)))
	}
	if !done.IsZero() {
		attrs = append(attrs, goipp.MakeAttribute("time-at-completed", goipp.TagInteger, goipp.Integer(done.Unix())))
	}
	return attrs
}

// filterAttributes keeps only the requested attributes (plus the
// attributes-charset / attributes-natural-language pair) from attrs.
// requested-attributes values of "all" keep everything.
func filterAttributes(attrs goipp.Attributes, req *goipp.Message) goipp.Attributes {
	a := findAttr(req.Operation, "requested-attributes")
	if a == nil {
		return attrs
	}
	requested := map[string]bool{}
	all := false
	for _, v := range a.Values {
		if s, ok := v.V.(goipp.String); ok {
			if s == "all" {
				all = true
				break
			}
			requested[string(s)] = true
		}
	}
	if all {
		return attrs
	}
	out := goipp.Attributes{}
	for _, attr := range attrs {
		if attr.Name == "attributes-charset" || attr.Name == "attributes-natural-language" || requested[attr.Name] {
			out = append(out, attr)
		}
	}
	return out
}
