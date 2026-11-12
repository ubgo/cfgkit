// Package jsonc adds JSONC (JSON with comments and trailing commas) files as a
// cfgkit structured source, parsed by github.com/ubgo/textdata/jsonc.
//
// It is a separate module because it carries a parser, and the core promises
// exactly one dependency. A program that never reads JSONC never compiles this
// package.
//
// What it adds over cfgkit.FromJSON:
//
//   - Comments, trailing commas and a BOM are accepted (jsonc.Strict() turns
//     them back into errors for plain JSON).
//   - ${VAR} expansion and plugins, when the caller asks for them through
//     jsonc options (jsonc.WithExpand, jsonc.WithLookup, jsonc.WithPlugin).
//     Nothing is expanded, and the process environment is never read, unless
//     the caller turns it on.
//   - Errors carry file:line:col: a syntax error and a value of the wrong type
//     both point at the offending bytes.
//   - Per-value provenance: Locate and Origins report the file:line each value
//     came from, which cfgkit's own record (one name per source) cannot.
package jsonc

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/ubgo/cfgkit"
	"github.com/ubgo/textdata/jsonc"
	"github.com/ubgo/textdata/path"
	"github.com/ubgo/textdata/pos"
)

// namePrefix starts the provenance name of every source built here, matching
// the "yaml:" and "toml:" prefixes of the sibling format modules.
const namePrefix = "jsonc:"

// Source is a JSONC document as a cfgkit structured source.
//
// It implements cfgkit.StructuredSource, so it goes straight into
// cfgkit.WithSources. It also remembers the document it last applied, so
// after a Load it can say on which line each value was written.
//
// A Source is safe for concurrent use. Apply re-reads the file every time, so
// a reload sees the current contents and Locate answers for them.
type Source struct {
	name string
	file string // the path for File, "" for Document
	src  []byte // the bytes for Document, nil for File
	opts []jsonc.Option

	mu  sync.Mutex
	doc *jsonc.Doc // the raw (unexpanded) document of the last Apply
}

// File returns a source that reads the JSONC file at path on every Apply.
//
// A MISSING file is not an error: it contributes nothing and the defaults
// stand, matching cfgkit.FromFiles and the other format modules. A file that
// exists but cannot be read, parsed or decoded IS an error, because a broken
// config must never look like an absent one.
//
// opts are textdata/jsonc options, passed through unchanged: jsonc.WithExpand
// to expand ${VAR} references before decoding, jsonc.WithLookup and
// jsonc.WithPlugin to say where names resolve, jsonc.Strict for plain JSON.
func File(path string, opts ...jsonc.Option) *Source {
	return &Source{name: namePrefix + path, file: path, opts: opts}
}

// Document returns a source over a JSONC document already in memory, e.g.
// from go:embed. name is used for provenance ("jsonc:"+name) and as the file
// name in error positions. doc is copied.
func Document(name string, doc []byte, opts ...jsonc.Option) *Source {
	// The file name goes first so a caller's own jsonc.WithFileName wins.
	all := append([]jsonc.Option{jsonc.WithFileName(name)}, opts...)
	return &Source{name: namePrefix + name, src: append([]byte{}, doc...), opts: all}
}

// Name implements cfgkit.StructuredSource: "jsonc:" plus the path or name.
// It is what cfgkit's Explain shows in the SOURCE column.
func (s *Source) Name() string { return s.name }

// Apply implements cfgkit.StructuredSource. It parses the document, expands it
// when jsonc.WithExpand was given, and merges its plain JSON onto dst with
// encoding/json, so fields the document does not mention keep their values.
func (s *Source) Apply(dst any) error {
	doc, err := s.parse()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.doc = doc
	s.mu.Unlock()
	if doc == nil {
		return nil // missing file: silent by contract
	}

	plain := doc
	if s.expands() {
		if plain, err = doc.Expanded(); err != nil {
			return s.fileErr(err)
		}
	}
	b, err := plain.JSON()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return s.decodeErr(doc, err)
	}
	return nil
}

// parse reads the document. It returns (nil, nil) for a missing file.
func (s *Source) parse() (*jsonc.Doc, error) {
	if s.file == "" {
		return jsonc.Parse(s.src, s.opts...)
	}
	d, err := jsonc.Open(s.file, s.opts...)
	if err != nil {
		return nil, err
	}
	if !d.Existed() {
		return nil, nil
	}
	return d, nil
}

// expands reports whether the caller passed jsonc.WithExpand.
//
// jsonc options are opaque functions and textdata/jsonc exposes the setting
// only through jsonc.Read, which decodes into a map (losing integers above
// 2^53). So the option is recognised by identity: every jsonc.WithExpand()
// returns the same function body, and reflect reports its code pointer. A
// test pins this, so a change on the textdata side fails loudly here instead
// of silently turning expansion off.
func (s *Source) expands() bool {
	want := reflect.ValueOf(jsonc.WithExpand()).Pointer()
	for _, o := range s.opts {
		if o != nil && reflect.ValueOf(o).Pointer() == want {
			return true
		}
	}
	return false
}

// fileName is the file errors and locations report.
func (s *Source) fileName() string {
	if s.file != "" {
		return s.file
	}
	return strings.TrimPrefix(s.name, namePrefix)
}

// fileErr gives an error without a position the file's name, so it never
// reads as if it came from nowhere. An error that already carries a position
// is returned as it is.
func (s *Source) fileErr(err error) error {
	if pos.First(err) != nil {
		return err
	}
	return &pos.Error{File: s.fileName(), Err: err}
}

// decodeErr turns an encoding/json error into one that points at the value in
// the source file. encoding/json reports offsets into the comment-free JSON,
// which do not match the file, so the position comes from the field path it
// names instead.
func (s *Source) decodeErr(doc *jsonc.Doc, err error) error {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		if loc, ok := locateKeys(doc, strings.Split(te.Field, ".")); ok {
			p, perr := doc.Pos(loc)
			if perr == nil {
				return &pos.Error{File: s.fileName(), Pos: p, Err: err}
			}
		}
	}
	return s.fileErr(err)
}

// Location is where one value was written.
type Location struct {
	// File is the file path, or the name given to Document.
	File string
	// Path is the value's canonical jsonc path, e.g. `server.port` or
	// `hosts[1]`.
	Path string
	// Pos is where the value starts; KeyPos is where its member name starts
	// (the value's position for an array element or the root).
	Pos    pos.Pos
	KeyPos pos.Pos
}

// String renders "file:line", pointing at the member name, which is the line
// a reader looks for.
func (l Location) String() string {
	return l.File + ":" + strconv.Itoa(l.KeyPos.Line)
}

// Locate reports where the value at a jsonc path (`a.b[2]` or `/a/b/2`) was
// written in the document the last Apply read. It reports false for a path the
// document does not hold, before the first Apply, for a missing file and for
// a document with syntax errors.
func (s *Source) Locate(p string) (Location, bool) {
	doc := s.current()
	if doc == nil {
		return Location{}, false
	}
	return locate(doc, s.fileName(), p)
}

// Locations reports every scalar and empty container of the last applied
// document, in document order.
func (s *Source) Locations() []Location {
	doc := s.current()
	if doc == nil {
		return nil
	}
	paths := doc.ValuePaths()
	out := make([]Location, 0, len(paths))
	for _, p := range paths {
		if l, ok := locate(doc, s.fileName(), p); ok {
			out = append(out, l)
		}
	}
	return out
}

// LocateField reports where the value of a Go field path ("Server.Port", as
// cfgkit's Field.Path names it) was written. cfg is the configuration struct
// or a pointer to it; it is used for its type only, to translate Go field
// names into JSON member names through `json:` tags the same way
// encoding/json does (case-insensitive, embedded structs promoted).
func (s *Source) LocateField(cfg any, goPath string) (Location, bool) {
	doc := s.current()
	if doc == nil {
		return Location{}, false
	}
	keys, ok := jsonKeys(reflect.TypeOf(cfg), goPath)
	if !ok {
		return Location{}, false
	}
	p, ok := locateKeys(doc, keys)
	if !ok {
		return Location{}, false
	}
	return locate(doc, s.fileName(), p)
}

// Origins maps the Go path of every field cfgkit attributes to this source
// to the place in the file it came from. Pass the Result and the struct of the
// same Load. Fields other sources won, and fields cfgkit does not record
// (those without an `env:` tag; use LocateField for them), are absent.
func (s *Source) Origins(res *cfgkit.Result, cfg any) map[string]Location {
	out := make(map[string]Location)
	if res == nil {
		return out
	}
	for _, f := range res.Fields() {
		if f.Source != s.name {
			continue
		}
		if l, ok := s.LocateField(cfg, f.Path); ok {
			out[f.Path] = l
		}
	}
	return out
}

// current returns the document of the last Apply.
func (s *Source) current() *jsonc.Doc {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc == nil || s.doc.Err() != nil {
		return nil
	}
	return s.doc
}

// locate builds the Location of path p in doc.
func locate(doc *jsonc.Doc, file, p string) (Location, bool) {
	vp, err := doc.Pos(p)
	if err != nil {
		return Location{}, false
	}
	kp, err := doc.KeyPos(p)
	if err != nil {
		return Location{}, false
	}
	return Location{File: file, Path: canonical(doc, p), Pos: vp, KeyPos: kp}, true
}

// canonical renders p in the dotted form ValuePaths uses. A JSON Pointer
// token made of digits addresses an index or a member depending on the
// container, so it is resolved against the document.
func canonical(doc *jsonc.Doc, p string) string {
	pp, err := path.ParseAny(p)
	if err != nil {
		return p
	}
	var out path.Path
	for _, seg := range pp {
		if seg.Kind == path.KindEither {
			if k, err := doc.Kind(out.String()); err == nil && k == jsonc.KindArray {
				seg = path.Index(seg.Index)
			} else {
				seg = path.Key(seg.Key)
			}
		}
		out = out.Append(seg)
	}
	return out.String()
}

// locateKeys resolves a list of JSON member names against doc the way
// encoding/json matches them: the last member whose name equals the key
// exactly or case-insensitively wins; a key under an array is an index. It
// returns the canonical path.
func locateKeys(doc *jsonc.Doc, keys []string) (string, bool) {
	var p path.Path
	for _, k := range keys {
		// encoding/json names an array element by its index ("hosts.0").
		if kind, err := doc.Kind(p.String()); err == nil && kind == jsonc.KindArray {
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 {
				return "", false
			}
			p = p.Append(path.Index(i))
			if !doc.Has(p.String()) {
				return "", false
			}
			continue
		}
		members, err := doc.Keys(p.String())
		if err != nil {
			return "", false
		}
		found := ""
		ok := false
		for _, m := range members {
			if m == k || strings.EqualFold(m, k) {
				found, ok = m, true
			}
		}
		if !ok {
			return "", false
		}
		p = p.Append(path.Key(found))
	}
	return p.String(), true
}

// jsonKeys translates a cfgkit Go field path into JSON member names using t's
// `json:` tags. Embedded structs without a tag are promoted, as both cfgkit
// (which leaves them out of the Go path) and encoding/json treat them.
func jsonKeys(t reflect.Type, goPath string) ([]string, bool) {
	var out []string
	for _, name := range strings.Split(goPath, ".") {
		t = deref(t)
		if t == nil || t.Kind() != reflect.Struct {
			return nil, false
		}
		keys, ft, ok := findField(t, name)
		if !ok {
			return nil, false
		}
		out = append(out, keys...)
		t = ft
	}
	return out, true
}

// findField finds the Go field name in struct t, looking through embedded
// structs. It returns the JSON member names leading to it (more than one when
// an embedded struct carries its own json name) and the field's type.
func findField(t reflect.Type, name string) ([]string, reflect.Type, bool) {
	for i := range t.NumField() {
		sf := t.Field(i)
		tag, hasTag := sf.Tag.Lookup("json")
		jname, _, _ := strings.Cut(tag, ",")
		if jname == "-" {
			continue
		}
		if sf.Anonymous {
			inner := deref(sf.Type)
			if inner == nil || inner.Kind() != reflect.Struct {
				continue
			}
			keys, ft, ok := findField(inner, name)
			if !ok {
				continue
			}
			if hasTag && jname != "" {
				// A named embedded struct is a nested object in JSON.
				keys = append([]string{jname}, keys...)
			}
			return keys, ft, true
		}
		if sf.Name != name || !sf.IsExported() {
			continue
		}
		if jname == "" {
			jname = sf.Name
		}
		return []string{jname}, sf.Type, true
	}
	return nil, nil, false
}

// deref strips pointers from t.
func deref(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
