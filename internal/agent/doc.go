// Package agent implements the agent loop on top of the client: the
// tool-calling loop (Agent), multi-turn conversations (Session with context
// window tracking and compaction), sub-agent delegation, skill wiring and
// web-search integration.
//
// It is an internal implementation detail. The public API lives in the parent
// package — import github.com/great-magician-01/callable instead.
package agent
