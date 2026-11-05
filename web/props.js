// The props Flowa knows about. `ready` becomes true once the prop's catalog loads
// (catalog/<id>.json; nunchaku is built in). `families` is only a fallback outline,
// shown if a catalog file is missing.
const PROPS = [
  { id: 'nunchaku', name: 'Nunchaku', kind: 'Two sticks joined by a cord or chain', ready: true },
  { id: 'poi', name: 'Poi', kind: 'A pair of weighted heads on tethers', ready: false,
    families: ['Planes, timing and direction', 'Weaves', 'Flowers and antispin', 'Isolations and stalls', 'Wraps', 'Tosses', 'Hybrids'] },
  { id: 'staff', name: 'Staff', kind: 'One long rigid staff', ready: false,
    families: ['Rotations and figure-8s', 'Passes and wraps', 'Contact rolls', 'Throws', 'Isolations'] },
  { id: 'doublestaff', name: 'Double staff', kind: 'Two shorter staffs, one in each hand', ready: false,
    families: ['Timing and direction', 'Weaves', 'Flowers', 'Hybrids', 'Throws'] },
  { id: 'buugeng', name: 'Buugeng', kind: 'A pair of S-shaped staffs', ready: false,
    families: ['Rotations', 'Symmetry and illusions', 'Weaves', 'Transitions'] },
];
