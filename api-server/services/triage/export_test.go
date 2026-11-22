package triage

// ObservedSourcesForWeighting exposes the observed-source set to the external
// test package, which can import the knowledge-graph flow sources without the
// import cycle an in-package test would hit.
var ObservedSourcesForWeighting = observedSourcesForWeighting
