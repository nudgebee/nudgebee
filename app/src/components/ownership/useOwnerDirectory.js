import { useEffect, useMemo, useState } from 'react';
import apiUserManagement from '@api1/user';

// Module-level cache so the directory is fetched at most once per session and
// shared across every picker instance. Reset on a failed load so a later open
// can retry, and on a full page reload.
let directoryPromise = null;

function loadDirectory() {
  if (!directoryPromise) {
    directoryPromise = Promise.all([apiUserManagement.listUsers({ limit: 1000 }), apiUserManagement.listUserGroups({ limit: 1000 })])
      .then(([usersRes, groupsRes]) => ({
        users: usersRes?.data || [],
        groups: groupsRes?.data?.usergroups_list?.rows || [],
      }))
      .catch((err) => {
        directoryPromise = null;
        throw err;
      });
  }
  return directoryPromise;
}

// Supplies the combined user + group option list for OwnerPicker. Loaded lazily
// the first time a picker mounts (i.e. when an assign/rule modal opens) — never
// on list mount. NOT used for badge display: badges read owner_name straight off
// the resolve/get response.
export default function useOwnerDirectory() {
  const [dir, setDir] = useState({ users: [], groups: [] });
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    loadDirectory()
      .then((data) => {
        if (!active) return;
        setDir(data);
        setLoading(false);
      })
      .catch(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  // Picker options: only ACTIVE users are assignable as owners (inactive /
  // suspended accounts are excluded). Users share display names (e.g. two
  // "Mangglesh Dagar" on different tenants), so the label appends the email to
  // disambiguate; `name` keeps the plain display name for compact chips/badges.
  const options = useMemo(() => {
    const userOpts = dir.users
      .filter((u) => !u.status || u.status === 'active')
      .map((u) => {
        const name = u.display_name || u.username;
        return { value: `user:${u.id}`, label: u.username && u.username !== name ? `${name} (${u.username})` : name, name, kind: 'user' };
      });
    const groupOpts = dir.groups.map((g) => ({ value: `group:${g.id}`, label: g.name, name: g.name, kind: 'group' }));
    return [...userOpts, ...groupOpts];
  }, [dir]);

  // id -> plain display name lookup, for surfaces that only have
  // owner_type/owner_id (e.g. the rules table, where RuleDto carries no name).
  // Built from ALL users (incl. inactive) so an already-assigned owner who later
  // went inactive still resolves to a name rather than a raw id.
  const ownerLabel = useMemo(() => {
    const map = {};
    dir.users.forEach((u) => {
      map[`user:${u.id}`] = u.display_name || u.username;
    });
    dir.groups.forEach((g) => {
      map[`group:${g.id}`] = g.name;
    });
    return (ownerType, ownerId) => map[`${ownerType}:${ownerId}`] || ownerId;
  }, [dir]);

  return { options, loading, ownerLabel };
}
