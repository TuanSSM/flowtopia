// Writes the fixture catalogs that the JavaScript and Go validators must both judge the same way:
// good-*.json must pass, bad-*.json must fail. Each bad file breaks one rule of
// docs/catalog-format.md.   node test/catalog-fixtures/make.js
const fs = require('fs'), path = require('path');
const skill = (id, tier, needs = [], extra = {}) => ({ id, tier, name: 'Skill ' + id, desc: 'A move used only in the fixture catalogs.', needs, track: 'one',
  steps: ['Do the first part.', 'Do the second part.'], watch: ['Keep the plane flat.'], drill: 'Ten slow repetitions a side.', refs: ['src'], ...extra });
const base = () => ({
  id: 'fixture', name: 'Fixture', about: 'A small catalog for testing the validators.', careNote: 'Learn this on foam.',
  branches: 'ABCDE'.split('').map(k => ({ k, name: 'Branch ' + k, blurb: 'Fixture branch' })),
  skills: [skill('A1', 1), skill('A2', 2, ['A1']), skill('B1', 1), skill('C1', 2, ['A1!']), skill('D1', 3, ['C1']), skill('E1', 1)],
  refs: { src: ['Fixture source', 'https://example.org/fixture'] },
  phases: [1, 2, 3].map(i => ({ id: 'p' + i, name: 'Phase ' + i, time: '1 month', skills: 'A1 B1', gate: 'A test you can pass or fail.', req: ['A1'] })),
});
const cases = {
  'good-base': c => c,
  'good-weeks': c => { c.weeks = [{ id: 'w1', from: 1, to: 2, label: 'Weeks 1-2', focus: 'Basics', result: 'A loop', gate: 'Eight of ten clean' }]; },
  'good-aliases-retired': c => { c.skills.push(skill('E2', 2, [], { retired: true })); c.aliases = { E9: 'E1' }; },
  'good-tier-warning': c => { c.skills.push(skill('B2', 1, ['D1'])); },
  'good-motion': c => { c.skills[0].motion = { plane: 'wall', direction: 'either', hands: 'one', grip: 'unknown', source: 'clip' }; },
  'bad-motion-value': c => { c.skills[0].motion = { plane: 'sideways' }; },
  'bad-motion-key': c => { c.skills[0].motion = { speed: 'fast' }; },
  // Shared concepts: the catalog names the ones it teaches, a skill lists up to three of those.
  'good-concepts': c => { c.concepts = { plane: 'The flat sheet a circle is drawn in: wall, face or table.', weave: 'Crossing from one side of the body to the other without stopping.' }; c.skills[0].concepts = ['plane']; c.skills[1].concepts = ['plane', 'weave']; },
  'good-concept-unused': c => { c.concepts = { isolation: 'One end of the prop stays still while the rest turns around it.' }; },
  'bad-concept-key': c => { c.concepts = { spin: 'Not one of the shared concepts, however long the sentence.' }; },
  'bad-concept-text': c => { c.concepts = { plane: 'Short.' }; },
  'bad-concept-dash': c => { c.concepts = { plane: 'The flat sheet a circle is drawn in \u2014 wall, face or table.' }; },
  'bad-concepts-shape': c => { c.concepts = ['plane']; },
  'bad-skill-concept-unknown': c => { c.concepts = { plane: 'The flat sheet a circle is drawn in: wall, face or table.' }; c.skills[0].concepts = ['weave']; },
  'bad-skill-concept-undeclared': c => { c.skills[0].concepts = ['plane']; },
  'bad-skill-concepts-twice': c => { c.concepts = { plane: 'The flat sheet a circle is drawn in: wall, face or table.' }; c.skills[0].concepts = ['plane', 'plane']; },
  'bad-skill-concepts-many': c => { c.concepts = Object.fromEntries(['plane', 'timing', 'weave', 'isolation'].map(k => [k, 'A sentence long enough to say what it means here.'])); c.skills[0].concepts = ['plane', 'timing', 'weave', 'isolation']; },
  'bad-skill-concepts-type': c => { c.concepts = { plane: 'The flat sheet a circle is drawn in: wall, face or table.' }; c.skills[0].concepts = ['plane', 1]; },
  'bad-skill-concepts-shape': c => { c.concepts = { plane: 'The flat sheet a circle is drawn in: wall, face or table.' }; c.skills[0].concepts = 'plane'; },
  'bad-not-object': () => [],
  'bad-missing-about': c => { delete c.about; },
  'bad-few-branches': c => { c.branches = c.branches.slice(0, 4); c.skills = c.skills.filter(s => 'ABCD'.includes(s.id[0])); },
  'bad-branch-key': c => { c.branches[0].k = 'ab'; },
  'bad-duplicate-branch': c => { c.branches[1].k = 'A'; },
  'bad-skill-id': c => { c.skills[0].id = 'a1'; },
  'bad-duplicate-skill': c => { c.skills[2].id = 'A1'; },
  'bad-tier': c => { c.skills[0].tier = 6; },
  'bad-track': c => { c.skills[0].track = 'both'; },
  'bad-steps': c => { c.skills[0].steps = []; },
  'bad-watch': c => { c.skills[0].watch = ['a', 'b', 'c', 'd']; },
  'bad-unknown-ref': c => { c.skills[0].refs = ['nowhere']; },
  'bad-long-dash': c => { c.skills[0].desc = 'A move — with a long dash in it.'; },
  'bad-unknown-need': c => { c.skills[1].needs = ['Z9']; },
  'bad-cycle': c => { c.skills[0].needs = ['A2']; },
  'bad-needs-retired': c => { c.skills.push(skill('E2', 2, [], { retired: true })); c.skills[5].needs = ['E2']; },
  'bad-empty-branch': c => { c.skills = c.skills.filter(s => s.id[0] !== 'E'); },
  'bad-ref-url': c => { c.refs.src = ['Fixture source', 'http://example.org/insecure']; },
  'bad-phase-id': c => { c.phases[1].id = 'p7'; },
  'bad-phase-skill': c => { c.phases[0].skills = 'A1 Q4'; },
  'bad-week-range': c => { c.weeks = [{ id: 'w1', from: 3, to: 2, label: 'Week 3', focus: 'Basics', result: 'A loop', gate: 'Eight of ten clean' }]; },
  'bad-alias-to-retired': c => { c.skills.push(skill('E2', 2, [], { retired: true })); c.aliases = { E9: 'E2' }; },
  'bad-alias-is-skill': c => { c.aliases = { A2: 'A1' }; },
};
for (const [name, f] of Object.entries(cases)) {
  const c = base(), out = f(c) || c;
  fs.writeFileSync(path.join(__dirname, name + '.json'), JSON.stringify(out, null, 1) + '\n');
}
console.log(Object.keys(cases).length, 'fixtures written');
