// Names shared by every page for the skill catalog in use. catalogs.js loads each prop's catalog
// from web/catalog/<prop>.json and useCatalog() points these at it.
let BRANCHES = [], SKILLS = [], PHASES = [], WEEKS = [], REFS = {};
const LEVELS = [
  {n:'Not started',d:'Not tried yet'},
  {n:'Learning',d:'Working on it, with drops'},
  {n:'Clean',d:'20 clean reps in a row, slowly. Unlocks what depends on it'},
  {n:'Reliable',d:'Works at speed, most attempts'},
  {n:'Performance',d:'Ready under pressure, in a routine'},
];
