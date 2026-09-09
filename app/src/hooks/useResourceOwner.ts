import { useEffect, useRef, useState } from 'react';
import apiOwnership from '@api1/ownership';

// A resolved OwnerResult, as returned by ownership_resolve. Snake_case because
// these come straight off the API and are handed to OwnerBadge unchanged.
export interface OwnerResult {
  resource_type?: string;
  resource_key?: string;
  found?: boolean;
  owner_type?: string;
  owner_id?: string;
  owner_name?: string;
  source?: string;
  via?: string;
}

// One rung of the ownership chain: a display label, the ref used to resolve it,
// and (after resolution) the owner set *directly* on that level.
export interface ChainLevel {
  level: string;
  resourceType: string;
  resourceKey: string;
  own: OwnerResult | null;
}

export interface ResourceOwner {
  /** The chain as requested, each rung carrying the owner set directly on it. */
  levels: ChainLevel[];
  /** Index of the rung that owns the resource, or -1 when nothing does. */
  effectiveIndex: number;
  /** The owner in force, with `via` restated relative to the resource itself. */
  effective: OwnerResult | null;
  loading: boolean;
  /** The resolve errored or was retired — render nothing rather than an empty state. */
  unresolvable: boolean;
}

// Separator for the response-matching key. NUL can't occur in a resource type or
// key, so it can't be forged by a value that happens to contain the separator.
const SEP = '\u0000';

// Restate `via` relative to the resource being asked about, so a badge renders the
// right inherited/rule hint: the first rung is the resource itself, a Namespace rung
// means inherited from the namespace, anything above means inherited from the cloud
// account (which the UI calls the cluster for k8s).
function viaFor(levels: ChainLevel[], index: number): string {
  if (index === 0) return 'self';
  return levels[index].level === 'Namespace' ? 'namespace' : 'cluster';
}

// derivedText explains, in one sentence, why the effective owner is who it is.
// Shared so every surface that shows ownership words it identically — the drilldown
// tab, the recommendation drawer and the investigation sidebar must never appear to
// disagree about the same resource.
export function derivedText(levels: ChainLevel[], effIndex: number): string {
  if (effIndex < 0) return 'No owner assigned yet.';
  if (effIndex === 0) {
    return levels[0].own?.source === 'rule' ? 'Matched by an ownership rule.' : 'Assigned directly to this resource.';
  }
  return levels[effIndex].level === 'Namespace' ? 'Inherited from the namespace owner.' : 'Inherited from the cloud account (cluster) owner.';
}

// sourceText says where an owner came from in one self-contained phrase, naming the
// rung as well as the mechanism. For surfaces too narrow to show the chain, so the
// level isn't lost — "Ownership rule, at namespace level" rather than a bare "rule"
// sitting next to a name, which reads as a second value.
export function sourceText(levels: ChainLevel[], effIndex: number): string {
  if (effIndex < 0) return 'No rule or assignment covers this resource';
  const byRule = levels[effIndex].own?.source === 'rule';
  if (effIndex === 0) {
    return byRule ? 'Ownership rule, on this resource' : 'Assigned directly to this resource';
  }
  const level = levels[effIndex].level.toLowerCase();
  return byRule ? `Ownership rule, at ${level} level` : `Inherited from the ${level} owner`;
}

/**
 * Resolves an ownership chain in one batched call and reports the owner in force.
 *
 * Takes the chain already built, rather than a resource id, because callers differ
 * in how they know which rungs apply — and getting that wrong fails silently. In
 * particular the k8s-vs-cloud split cannot be inferred from "is there a namespace":
 * investigation events carry a `subject_namespace` for cloud alerts too, holding the
 * cloud service name (`EC2`), so inferring here would ask for a nonsense namespace
 * rung. Each caller states its own chain; this hook only resolves it.
 *
 * `levels` may be a fresh array each render — it is compared by the
 * (resourceType, resourceKey) pairs it contains, not by identity.
 */
export default function useResourceOwner(levels: ChainLevel[]): ResourceOwner {
  const [resolved, setResolved] = useState<ChainLevel[]>([]);
  const [loading, setLoading] = useState(true);
  // Set when there is no valid result to show — either the resolve errored, or the
  // request was retired (the view closed or moved to another resource). Either way
  // the caller should render nothing rather than sit on a spinner or wrongly claim
  // the resource is unowned.
  const [unresolvable, setUnresolvable] = useState(false);

  // Each fetch claims a token; a response only applies if no newer fetch has started.
  // Host views are often persistent — a drawer or sidebar swaps its subject prop
  // rather than remounting — so without this a slow response for the previous
  // subject can land after the current one's and show the wrong owner.
  const requestIdRef = useRef(0);

  // Identity of the chain, so a caller passing a fresh array each render doesn't refetch.
  const key = levels.map((l) => `${l.resourceType}${SEP}${l.resourceKey}`).join('|');

  useEffect(() => {
    const requestId = ++requestIdRef.current;
    if (levels.length === 0) {
      setResolved([]);
      setLoading(false);
      return undefined;
    }
    // Aborted when the host unmounts or moves to another resource, so the request
    // stops on the wire instead of running to completion unwatched.
    const controller = new AbortController();
    setLoading(true);
    setUnresolvable(false);
    apiOwnership
      .resolveOwners(
        levels.map((l) => ({ resource_type: l.resourceType, resource_key: l.resourceKey })),
        controller.signal
      )
      .then((results: OwnerResult[]) => {
        if (requestId !== requestIdRef.current) return;
        // Match responses by (type, key) rather than by position. The resolver
        // returns results aligned to the request, but a short result set would
        // shift every level onto the wrong rung and silently show the wrong
        // owner — a wrong name here is worse than a missing one.
        const byKey = new Map((results || []).map((r) => [`${r.resource_type}${SEP}${r.resource_key}`, r]));
        // A level's "own" owner is one resolved directly on it (via self), not one
        // it inherited from a level above — that's what makes the chain readable.
        setResolved(
          levels.map((l) => {
            const r = byKey.get(`${l.resourceType}${SEP}${l.resourceKey}`);
            return { ...l, own: r?.found && r.via === 'self' ? r : null };
          })
        );
        setLoading(false);
      })
      .catch(() => {
        if (requestId !== requestIdRef.current) return;
        setUnresolvable(true);
        setLoading(false);
      });
    // Retire this request on the way out: bump the token so an in-flight response
    // can't apply, abort so it stops on the wire, and mark unresolvable. The last
    // part matters for an instance that somehow outlives its cleanup (a stale
    // subtree, or a hot reload) — it stops showing a spinner forever.
    return () => {
      requestIdRef.current++;
      controller.abort();
      setUnresolvable(true);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const effectiveIndex = resolved.findIndex((l) => l.own);
  const effective = effectiveIndex >= 0 ? { ...(resolved[effectiveIndex].own as OwnerResult), via: viaFor(resolved, effectiveIndex) } : null;

  return { levels: resolved, effectiveIndex, effective, loading, unresolvable };
}
