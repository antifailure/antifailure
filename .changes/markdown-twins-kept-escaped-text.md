# fixed

A page that showed escaped markup as text had a markdown twin that said
something else. The site writes a plain markdown copy of every page for
readers and language models, and the text it took from the HTML was decoded
in the wrong order: `&amp;` became `&` first, so the next step then read the
result as a second entity. A page that displayed `&lt;b&gt;`, which the HTML
carries as `&amp;lt;b&amp;gt;`, came out in its twin as `<b>`, and a displayed
`&quot;` came out as a bare quote mark. The twin quietly stated markup the page
never showed.

The check that compares every page against its twin could not see it, because
it decoded with the same order, so both sides were wrong in the same way and
agreed. The writer and the check now share one decoder, which turns `&amp;`
back into `&` last, and a test renders a page carrying exactly those strings
and requires that both its twin and the check keep them as shown.
