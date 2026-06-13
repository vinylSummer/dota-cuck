// Friend-status derivation. Mirrors the worker's derive_status: being in a Dota
// match is the core signal, and the only state from which spectating is allowed.

// canSpectate is true only when the friend is currently in a Dota match.
export function canSpectate(friend) {
  return !!friend && friend.in_match === true;
}

// statusLabel renders the friend's presence for the list row.
export function statusLabel(friend) {
  if (!friend || !friend.online) return 'offline';
  return friend.in_match ? 'in match' : 'online';
}
