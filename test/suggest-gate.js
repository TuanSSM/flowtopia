// The gate on the committed suggestion weights (web/suggest-weights.json). With at least 30 shared
// marked transitions the fitted score must reach an AUC of 0.75 on held-out pairs and beat the
// prerequisite-only baseline; with fewer, the weights must be the starting ones and marked early, so
// the interface says suggestions are not yet checked against real combos.
const fs = require('fs'), path = require('path'), S = require('../web/suggest.js'), { MIN_PAIRS } = require('../sim/fit-suggest.js');
const W = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'web', 'suggest-weights.json'), 'utf8'));
let problem = '';
if (W.pairs >= MIN_PAIRS) {
  if (W.early) problem = 'enough pairs to fit, but the weights are still marked early';
  else if (!(W.auc >= 0.75)) problem = `AUC ${W.auc} is below 0.75`;
  else if (!(W.auc > (W.baseline ?? 0))) problem = `AUC ${W.auc} does not beat the prerequisite baseline ${W.baseline}`;
} else if (!W.early || JSON.stringify(W.weights) !== JSON.stringify(S.DEFAULT)) problem = `only ${W.pairs} pairs: the weights must be the starting ones and marked early`;
console.log(problem ? 'FAIL suggestion gate: ' + problem : `PASS suggestion gate (${W.early ? `early, ${W.pairs} pairs` : `AUC ${W.auc} against baseline ${W.baseline}, ${W.pairs} pairs`})`);
process.exit(problem ? 1 : 0);
