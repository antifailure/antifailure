# changed

The product and solutions pages draw the run they describe.

Six figures on /product and its five feature pages, and the twin lifecycle on
the homepage, were redrawn from the shared figure kit: a plan and its safer
sequence, the isolated run boundary and the decision loop that closes it, a
shaped load run compared against production baselines, the firewall's mock,
capture and deny modes, and a subset that expands by foreign-key closure. Each
one carries the caption it needs, so a number that was chosen for an
illustration reads as chosen rather than measured, and every figure that reads
as a measurement now names its source.

Four classes written on those pages set a property another class on the same
element had already set, so they did nothing. `cn` in this repository is a
plain join rather than tailwind-merge: a class passed to a component lands
beside the component's own and the cascade picks between them, which has
nothing to do with which one the author wrote last. Three eyebrows on /product
meant to carry the sage signal rendered in the same grey as the ones that carry
none, a stopped seal on /product/firewall rendered white instead of the pale
red that marks it stopped, and the lead paragraph of all four solutions heroes
rendered in the body grey instead of black. Each of those now emits one class
for the property, chosen by a ternary or by a prop on the component, so the
colour on the page is the colour somebody asked for.
