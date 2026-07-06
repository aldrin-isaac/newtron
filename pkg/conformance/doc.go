// Package conformance holds machine checks that keep the governance corpus
// consistent with itself and with the codebase — drift detection for the
// repository's own intent, in the same spirit the product applies to devices.
//
// The first resident is the DESIGN_PRINCIPLES crosswalk check: the universal
// document (docs/DESIGN_PRINCIPLES.md) owns each concept, the newtron document
// (docs/DESIGN_PRINCIPLES_NEWTRON.md) owns its application, and the mapping
// between them lives in exactly one place — the Universal § column of the
// newtron document's summary table. The tests here fail when a principle is
// added, removed, or renumbered in either document without the crosswalk (and
// both summary tables) following.
package conformance
