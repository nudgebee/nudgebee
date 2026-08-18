package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

/*
Table columns — a panel's per-column display settings, and the single place they
go.

ONE list, `options.columns`. An entry with `name` configures a column the query
returns; an entry without one describes a column the panel ADDS. Everything a
column can do — hiding, a link, a title, how the value formats — is an optional
key on that entry, so the next attribute is one more key rather than another
top-level array keyed by column name for every reader to join against. That is
what `link_columns` / `hidden_columns` were, and upgradeTableOptions folds them
into this shape on read.

The renderer's counterpart is app/src/components/k8s/dashboards/panelColumns.ts.
Both ends validate, for different reasons: this refuses to STORE a definition
that would render a dangerous link, and the frontend refuses to render one,
because a definition can also arrive through dashboard import.
*/

// ColumnLink is where a row can take you. An object rather than a bare string so
// link behaviour can grow — a tooltip, a condition, a target — without changing
// the column that carries it.
type ColumnLink struct {
	// An in-app path with `{{column}}` placeholders the renderer fills per row.
	Url string `json:"url"`
}

// TableColumn is one column's settings.
type TableColumn struct {
	// Query column this configures. Empty = a column the panel adds.
	Name string `json:"name,omitempty"`
	// Header text; on an added column it is also every cell's text.
	Title string `json:"title,omitempty"`
	// "hidden" keeps the column queried but off-screen — a link built from an id
	// needs the id in the result even when nobody should read it.
	Visibility string `json:"visibility,omitempty"`
	// How the value renders, overriding what the frontend's column registry
	// decides. Deliberately NOT validated here: the vocabulary is a display
	// concern the renderer owns, and an unknown value falls back to plain text
	// rather than breaking a save.
	Format string      `json:"format,omitempty"`
	Link   *ColumnLink `json:"link,omitempty"`
}

// TableOptions is the subset of a panel's `options` this package understands.
// Anything else in there is carried through untouched — the renderer owns its
// own display settings.
type TableOptions struct {
	Columns []TableColumn `json:"columns,omitempty"`
	// The two arrays Columns replaced. Read so a definition written before the
	// change still renders; upgradeTableOptions folds them forward and clears
	// them, so `omitempty` drops them the next time the dashboard is written.
	// Never set these.
	LegacyLinkColumns   []legacyLinkColumn `json:"link_columns,omitempty"`
	LegacyHiddenColumns []string           `json:"hidden_columns,omitempty"`
}

// legacyLinkColumn is one entry of the superseded `link_columns` array.
type legacyLinkColumn struct {
	Column string `json:"column,omitempty"`
	Title  string `json:"title,omitempty"`
	Url    string `json:"url"`
}

// VisibilityHidden keeps a column in the query but off the table — a link built
// from a row's id needs that id in the result even when nobody should read it.
const VisibilityHidden = "hidden"

/*
upgradeColumns returns a panel's column settings in the current shape, folding
the two superseded arrays in when that is all a definition carries.

Runs on READ and at validation rather than as a data migration, the same way
upgradeDefinition handles the panel model: the JSON shape evolves with a
code-side upgrader, and the next write of the dashboard persists the new shape.
The frontend mirrors this in panelColumns.ts, for a definition that arrives by
import and so never passes through here.
*/
func upgradeColumns(opts TableOptions) []TableColumn {
	if len(opts.Columns) > 0 {
		return opts.Columns
	}
	columns := make([]TableColumn, 0, len(opts.LegacyHiddenColumns)+len(opts.LegacyLinkColumns))
	at := map[string]int{}
	for _, name := range opts.LegacyHiddenColumns {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		at[name] = len(columns)
		columns = append(columns, TableColumn{Name: name, Visibility: VisibilityHidden})
	}
	for _, link := range opts.LegacyLinkColumns {
		column, title := strings.TrimSpace(link.Column), strings.TrimSpace(link.Title)
		switch {
		case column != "":
			// A link on a column that is also hidden merges into that one entry:
			// two entries for one column is the ambiguity this shape removes, and
			// validation rejects it.
			if i, ok := at[column]; ok {
				columns[i].Link = &ColumnLink{Url: link.Url}
				continue
			}
			at[column] = len(columns)
			columns = append(columns, TableColumn{Name: column, Link: &ColumnLink{Url: link.Url}})
		case title != "":
			columns = append(columns, TableColumn{Title: title, Link: &ColumnLink{Url: link.Url}})
		}
	}
	return columns
}

/*
upgradeTableOptions rewrites one panel's `options` into the current shape, so a
dashboard read today and written back tomorrow no longer carries the old arrays.
Idempotent: a panel already in the new shape is left exactly as it is.
*/
func upgradeTableOptions(p *Panel) {
	if p.Options == nil {
		return
	}
	_, hasLinks := p.Options["link_columns"]
	_, hasHidden := p.Options["hidden_columns"]
	if !hasLinks && !hasHidden {
		return
	}
	opts, err := decodeTableOptions(p.Options)
	if err != nil {
		// Unreadable options are not this function's to repair — validation
		// reports them, and dropping them here would lose an author's work.
		return
	}
	columns := upgradeColumns(opts)
	delete(p.Options, "link_columns")
	delete(p.Options, "hidden_columns")
	if len(columns) == 0 {
		delete(p.Options, "columns")
		return
	}
	// Back through JSON so `options` stays a plain map — the same untyped blob
	// every other reader of it expects.
	encoded, err := json.Marshal(columns)
	if err != nil {
		return
	}
	var generic []any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return
	}
	p.Options["columns"] = generic
}

// decodeTableOptions reads the link/hidden settings out of a panel's options.
// Round-tripped through JSON so the `json` tags govern, the same way the entity
// query is decoded.
func decodeTableOptions(options map[string]any) (TableOptions, error) {
	var opts TableOptions
	if len(options) == 0 {
		return opts, nil
	}
	body, err := json.Marshal(options)
	if err != nil {
		return opts, fmt.Errorf("options are not readable: %w", err)
	}
	if err := json.Unmarshal(body, &opts); err != nil {
		return opts, fmt.Errorf("options are not readable: %w", err)
	}
	return opts, nil
}

/*
validLinkUrl reports whether a link column may point at this URL.

Relative in-app paths ONLY. A panel is authored by one user and rendered by
every viewer of the dashboard, so an unrestricted href is a stored-XSS vector
(`javascript:…`) and an off-site one is a phishing vector. A leading `//` is
protocol-relative — another origin wearing a path's clothes — and whitespace or
control characters are how a scheme gets smuggled past a prefix check, since
browsers strip them before parsing.

A BACKSLASH is the same hole wearing a different hat: the URL spec treats `\`
as `/` for http(s), so `/\evil.example` parses as `//evil.example` — the
protocol-relative form this rejects two lines up — and navigates off-site.
*/
func validLinkUrl(url string) bool {
	if !strings.HasPrefix(url, "/") || strings.HasPrefix(url, "//") {
		return false
	}
	for _, r := range url {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' {
			return false
		}
	}
	return true
}

// validateTableOptions rejects column settings the renderer could not draw, at
// save rather than on every viewer's screen.
func validateTableOptions(p Panel) error {
	opts, err := decodeTableOptions(p.Options)
	if err != nil {
		return fmt.Errorf("panel %q: %w", p.Title, err)
	}
	columns := upgradeColumns(opts)
	if len(columns) == 0 {
		return nil
	}
	// Every other visualisation draws a series, which has no row to open and no
	// column to hide.
	if p.Type != VizTable {
		return fmt.Errorf("panel %q: column settings only apply to a table panel", p.Title)
	}
	seen := map[string]bool{}
	for _, c := range columns {
		name, title := strings.TrimSpace(c.Name), strings.TrimSpace(c.Title)
		if name == "" && title == "" {
			return fmt.Errorf("panel %q: a column setting needs the name of a column it configures, or a title for the column it adds", p.Title)
		}
		// Two entries for one column leaves no answer to which one wins.
		if name != "" {
			if seen[name] {
				return fmt.Errorf("panel %q: column %q is configured twice", p.Title, name)
			}
			seen[name] = true
		}
		if c.Visibility != "" && c.Visibility != VisibilityHidden {
			return fmt.Errorf("panel %q: column %q has an unknown visibility %q", p.Title, columnLabel(name, title), c.Visibility)
		}
		if c.Link != nil && !validLinkUrl(strings.TrimSpace(c.Link.Url)) {
			return fmt.Errorf("panel %q: the link on %q must point at a path inside the product, e.g. /investigate?id={{id}}", p.Title, columnLabel(name, title))
		}
		// An added column is only ever a link or a label; without a link it would
		// render a column of identical text, which is not something to save.
		if name == "" && c.Link == nil {
			return fmt.Errorf("panel %q: the added column %q needs a link", p.Title, title)
		}
	}
	return nil
}

// columnLabel names a column for an error message, however it was identified.
func columnLabel(name, title string) string {
	if name != "" {
		return name
	}
	return title
}
