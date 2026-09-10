package cfgkit

import (
	"reflect"
)

// Structured sources report nothing about which fields they set. encoding/json
// unmarshals silently, and a caller's own yaml.Unmarshal is just as opaque. So
// provenance for them is recovered by OBSERVATION rather than instrumentation:
// snapshot every leaf, apply the source, and attribute whatever changed.
//
// Doing it this way rather than parsing the document has one decisive
// advantage: it works for every structured source ever written, including a
// caller's five-line StructuredFunc wrapping a format cfgkit has never heard
// of. Parsing JSON paths would have covered FromJSON alone.
//
// The known limit: a source that writes a value identical to the one already
// there is invisible, and the earlier origin stands. That is the correct
// answer for "which source decided this value" in every case that matters —
// the value is the same either way — and no cheaper method distinguishes them.

// snapshotLeaves records every leaf value in the tree, keyed by Go path.
//
// It walks independently of the env tags, because a structured source may fill
// a field that no flat key binds — that field still deserves an origin.
func snapshotLeaves(v reflect.Value, claimed claimFn) map[string]string {
	out := make(map[string]string)
	collectLeaves(v, "", out, claimed)
	return out
}

// collectLeaves is snapshotLeaves' recursion.
func collectLeaves(v reflect.Value, path string, out map[string]string, claimed claimFn) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		fv := v.Field(i)
		if !visitable(sf, fv) {
			continue
		}

		childPath := sf.Name
		if path != "" {
			childPath = path + "." + sf.Name
		}
		if sf.Anonymous {
			// An embedded struct contributes its children at the parent's path,
			// matching how walk reports them, so the two agree on names.
			childPath = path
		}

		if isWalkable(fv, claimed) {
			target := fv
			if target.Kind() == reflect.Pointer {
				if target.IsNil() {
					// A nil section has no leaves yet. Record its nil-ness so
					// allocation by a structured source registers as a change.
					out[childPath] = "<nil>"
					continue
				}
				target = target.Elem()
			}
			collectLeaves(target, childPath, out, claimed)
			continue
		}

		out[childPath] = formatValue(fv, newDelims("", ""))
	}
}

// attributeChanges records src as the origin of every leaf whose value differs
// between before and after.
// It also records the change as SOURCED, which is what keeps §6.4 honest for
// structured sources. Pruning asks "did a source set anything beneath this
// section", and before this it only ever heard from the flat binder — so a
// section configured entirely by a JSON or Pkl document was pruned back to nil
// and the feature silently did not start, with the document's own values
// thrown away.
func attributeChanges(before, after map[string]string, src string, res *Result) {
	for path, now := range after {
		if before[path] != now {
			res.origins[path] = src
			res.markSourced(path)
		}
	}
}
