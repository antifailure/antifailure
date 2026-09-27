/* The documentation build is Astro, separate from the Next marketing build.
 * This small bridge uses the same source-position keys as the marketing pages.
 * It keeps the documentation's authored Markdown as the default and stores only
 * explicit edits in the shared website document. */
(() => {
  const API = 'https://app.antifailure.dev/v1/website/published';
  const PAGE_API = `${API}?path=${encodeURIComponent(`/${location.pathname.split('/').filter(Boolean).join('/')}`)}`;
  const blocked = "script,style,noscript,pre,code,textarea,input,select,option,button,svg,[aria-hidden='true'],[contenteditable],[data-cms-key]:not([data-cms-sitewide-key])";
  const page = location.pathname.split('/').filter(Boolean).join('-').replace(/[^a-zA-Z0-9_-]+/g, '-') || 'home';
  const sectionId = `page-${page}`;
  const params = new URLSearchParams(location.search);
  const parentOrigin = params.get('parentOrigin');
  const session = params.get('session');
  const trustedParent = parentOrigin === 'https://app.antifailure.dev' ||
    (location.hostname === 'localhost' && parentOrigin === 'http://localhost:4332');
  const preview = window.parent !== window && trustedParent && /^[a-f0-9-]{36}$/i.test(session || '');
  const nodes = new Map();
  const images = new Map();
  let currentDocument = { fields: {}, styles: {} };
  let assetUrls = {};
  let editing = false;
  let revision = -1;
  const styleElement = document.createElement('style');
  styleElement.setAttribute('data-cms-styles', '');
  document.head.appendChild(styleElement);

  function hash(value) {
    let first = 2166136261;
    let second = 1831565813;
    for (let index = 0; index < value.length; index += 1) {
      first = Math.imul(first ^ value.charCodeAt(index), 16777619);
      second = Math.imul(second ^ value.charCodeAt(index), 2246822519);
    }
    return `${(first >>> 0).toString(36)}${(second >>> 0).toString(36)}`;
  }
  function pathOf(node, root) {
    const parts = [];
    let element = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
    while (element && element !== root) {
      const siblings = element.parentElement ? [...element.parentElement.children].filter((child) => child.tagName === element.tagName) : [];
      parts.unshift(`${element.tagName.toLowerCase()}${siblings.indexOf(element)}`);
      element = element.parentElement;
    }
    if (node.nodeType === Node.TEXT_NODE) parts.push(`t${[...node.parentNode.childNodes].filter((child) => child.nodeType === Node.TEXT_NODE).indexOf(node)}`);
    return parts.join('/');
  }
  function scan() {
    const root = document.querySelector('main');
    if (!root) return [];
    root.setAttribute('data-cms-section', sectionId);
    const fields = [];
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      const parent = node.parentElement;
      if (!parent || parent.closest(blocked)) continue;
      const text = node.textContent || '';
      const value = text.trim();
      if (value.length < 2 || value.length > 3000 || !/[a-zA-Z0-9]/.test(value)) continue;
      const key = `page.${page}.text.h${hash(pathOf(node, root))}`;
      let original = nodes.get(key);
      if (!original || original.node !== node) {
        const start = text.indexOf(value);
        original = { node, value, prefix: text.slice(0, start), suffix: text.slice(start + value.length) };
        nodes.set(key, original);
      }
      fields.push({ key, label: `${parent.tagName.toLowerCase()} · ${original.value.slice(0, 72)}`, kind: 'text', sectionId, defaultValue: original.value });
      if (!parent.hasAttribute('data-cms-key')) {
        parent.setAttribute('data-cms-key', key);
        parent.setAttribute('data-cms-sitewide-key', 'true');
      }
    }
    for (const element of root.querySelectorAll('img')) {
      if (element.closest('[data-cms-key],[aria-hidden="true"]')) continue;
      const src = element.getAttribute('src') || '';
      if (!/^\/(?!\/)/.test(src)) continue;
      const key = `page.${page}.image.h${hash(pathOf(element, root))}`;
      const original = { element, src, srcSet: element.getAttribute('srcset'), alt: element.getAttribute('alt') || '', display: element.style.getPropertyValue('display'), displayPriority: element.style.getPropertyPriority('display'), hidden: element.hidden };
      images.set(key, original);
      element.setAttribute('data-cms-key', key);
      element.setAttribute('data-cms-sitewide-key', 'true');
      fields.push({ key, label: `Image · ${original.alt || page}`, kind: 'media', sectionId, defaultValue: { type: 'media', source: 'builtin', src, kind: 'image', alt: original.alt } });
    }
    return fields;
  }
  const fields = scan();
  const sourceTitle = document.querySelector('meta[name="af-cms-source-title"]')?.content || document.title;
  const sourceDescription = document.querySelector('meta[name="af-cms-source-description"]')?.content || document.querySelector('meta[name="description"]')?.content || '';
  fields.push({ key: `seo.${page}.title`, label: 'Search and social title', kind: 'text', sectionId, defaultValue: sourceTitle });
  fields.push({ key: `seo.${page}.description`, label: 'Search and social description', kind: 'text', sectionId, defaultValue: sourceDescription });
  const chrome = new Map();
  function addChrome(key, element, group, kind, value) {
    const existing = chrome.get(key);
    if (existing) { existing.elements.push(element); return; }
    chrome.set(key, { elements: [element], value, kind });
    fields.push({ key, label: `${group} ${kind === 'url' ? 'link' : ''} · ${element.textContent?.trim() || key}`, kind, sectionId: group, defaultValue: value });
  }
  for (const element of document.querySelectorAll('[data-cms-section="header"] [data-cms-key],[data-cms-section="footer"] [data-cms-key]')) {
    const key = element.getAttribute('data-cms-key');
    const group = element.closest('[data-cms-section]')?.getAttribute('data-cms-section');
    if (!key || !group) continue;
    addChrome(key, element, group, 'text', element.textContent || '');
  }
  for (const element of document.querySelectorAll('[data-cms-section="header"] [data-cms-href],[data-cms-section="footer"] [data-cms-href]')) {
    const key = element.getAttribute('data-cms-href');
    const group = element.closest('[data-cms-section]')?.getAttribute('data-cms-section');
    if (!key || !group) continue;
    addChrome(key, element, group, 'url', element.getAttribute('href') || '/');
  }
  fields.push({ key: 'header.logo.image', label: 'Header logo image', kind: 'media', sectionId: 'header', defaultValue: null });
  fields.push({ key: 'header.logo.label', label: 'Header logo accessible label', kind: 'text', sectionId: 'header', defaultValue: document.querySelector('.af-wordmark')?.getAttribute('aria-label') || 'Antifailure' });
  const fonts = [
    { key: 'inter', label: 'Inter', family: 'Inter, Arial, sans-serif' },
    { key: 'geist', label: 'Geist', family: 'Geist, Arial, sans-serif' },
    { key: 'geist-mono', label: 'Geist Mono', family: 'Geist Mono, monospace' },
    { key: 'system-serif', label: 'Editorial serif', family: 'Georgia, serif' },
    { key: 'system-sans', label: 'System sans', family: 'Arial, sans-serif' },
    { key: 'system-mono', label: 'System monospace', family: 'Courier New, monospace' },
  ];
  const sourceSections = [{ id: sectionId, label: location.pathname, group: 'page' }, { id: 'header', label: 'Header', group: 'header' }, { id: 'footer', label: 'Footer', group: 'footer' }];
  const manifest = { schemaVersion: 1, sourceVersion: document.querySelector('meta[name="af-source-version"]')?.content || 'docs-source', fields, sections: sourceSections.slice(), collections: [], fonts, builtinAssets: [] };
  const sourceFields = fields.slice();
  const markdown = document.querySelector('.sl-markdown-content');
  const blocksHost = document.createElement('div');
  blocksHost.className = 'cms-doc-blocks';
  markdown?.after(blocksHost);
  let blockSignature = '';
  function pagePrefix() {
    let value = 2166136261;
    for (const letter of location.pathname) value = Math.imul(value ^ letter.charCodeAt(0), 16777619);
    return `custom-p${(value >>> 0).toString(36)}-`;
  }
  function field(id, part, fallback) {
    const value = currentDocument.fields?.[`${id}.${part}`];
    return value === undefined ? fallback : value;
  }
  function safeLink(value) {
    if (typeof value !== 'string' || !value || value.length > 2048 || /[\s\\<>"`]/.test(value) || value.startsWith('//')) return false;
    if (value.startsWith('/') || value.startsWith('#')) return true;
    if (!/^https?:\/\//i.test(value)) return false;
    try {
      const url = new URL(value);
      return ['http:', 'https:'].includes(url.protocol) && !!url.hostname && !url.username && !url.password;
    } catch { return false; }
  }
  function renderRich(value, key) {
    const wrapper = document.createElement('div');
    wrapper.className = 'cms-doc-rich';
    wrapper.setAttribute('data-cms-key', key);
    if (typeof value === 'string') { wrapper.append(element('p', value)); return wrapper; }
    function render(node, depth) {
      if (!node || typeof node !== 'object' || depth > 12) return null;
      if (node.type === 'hardBreak') return document.createElement('br');
      if (node.type === 'text') {
        let result = document.createTextNode(typeof node.text === 'string' ? node.text : '');
        for (const mark of Array.isArray(node.marks) ? node.marks : []) {
          const tag = { bold: 'strong', italic: 'em', emphasis: 'em', underline: 'u', code: 'code', link: 'a' }[mark?.type];
          if (!tag) continue;
          const outer = document.createElement(tag);
          if (tag === 'a' && safeLink(mark.attrs?.href)) outer.setAttribute('href', mark.attrs.href);
          else if (tag === 'a') continue;
          outer.append(result); result = outer;
        }
        return result;
      }
      const tag = { paragraph: 'p', bulletList: 'ul', orderedList: 'ol', listItem: 'li' }[node.type];
      if (!tag) return null;
      const outer = document.createElement(tag);
      for (const child of Array.isArray(node.content) ? node.content : []) {
        const rendered = render(child, depth + 1);
        if (rendered) outer.append(rendered);
      }
      return outer;
    }
    for (const child of Array.isArray(value?.content) ? value.content : []) {
      const rendered = render(child, 0);
      if (rendered) wrapper.append(rendered);
    }
    return wrapper;
  }
  function element(tag, text, key) {
    const node = document.createElement(tag);
    node.textContent = text;
    if (key) node.setAttribute('data-cms-key', key);
    return node;
  }
  function mediaFor(id, kind) {
    const value = field(id, 'media', null);
    if (!value || value.type !== 'media') return null;
    let url = null;
    if (value.source === 'asset' && /^[a-f0-9-]{36}$/i.test(value.assetId || '')) {
      const candidate = assetUrls[value.assetId] || `https://app.antifailure.dev/v1/website/media/${value.assetId}`;
      try {
        const checked = new URL(candidate);
        if (checked.origin === 'https://app.antifailure.dev' || location.hostname === 'localhost' && checked.origin === 'http://localhost:4332') url = checked.href;
      } catch { /* Bad media references never become requests. */ }
    } else if (value.source === 'builtin' && typeof value.src === 'string' && /^\/(?!\/)/.test(value.src)) url = value.src;
    if (!url) return null;
    const node = document.createElement(kind === 'video' ? 'video' : 'img');
    node.src = url;
    if (kind === 'video') { node.controls = true; node.playsInline = true; node.preload = 'metadata'; }
    else { node.alt = value.alt || ''; node.loading = 'lazy'; }
    node.setAttribute('data-cms-key', `${id}.media`);
    return node;
  }
  function embedDocument(html, css, javascript) {
    const policy = "default-src 'none'; script-src https: 'unsafe-inline'; style-src https: 'unsafe-inline'; img-src https: data: blob:; font-src https: data:; connect-src https:; media-src https: blob:; frame-src 'none'; form-action 'none'; base-uri 'none'";
    return `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${policy}"><meta name="viewport" content="width=device-width,initial-scale=1"><style>html,body{margin:0;min-height:100%}${css.replace(/<\/style/gi, '<\\/style')}</style></head><body>${html}<script>${javascript.replace(/<\/script/gi, '<\\/script')}</script></body></html>`;
  }
  function renderBlocks() {
    const sections = Array.isArray(currentDocument.sections?.custom) ? currentDocument.sections.custom : [];
    const hidden = new Set(Array.isArray(currentDocument.sections?.hidden) ? currentDocument.sections.hidden : []);
    const scoped = sections.filter((item) => item && item.group === 'page' && typeof item.id === 'string' && item.id.startsWith(pagePrefix()));
    const ordered = scoped.slice();
    for (const move of Array.isArray(currentDocument.sections?.moves) ? currentDocument.sections.moves : []) {
      if (!move || typeof move.id !== 'string') continue;
      const index = ordered.findIndex((item) => item.id === move.id);
      if (index < 0) continue;
      const [item] = ordered.splice(index, 1);
      const anchor = ordered.findIndex((entry) => entry.id === move.after);
      ordered.splice(move.after === null || move.after === sectionId ? 0 : anchor < 0 ? ordered.length : anchor + 1, 0, item);
    }
    const root = document.querySelector('main');
    const titlePanel = root?.querySelector(':scope > .content-panel');
    if (titlePanel) titlePanel.hidden = hidden.has(sectionId);
    if (markdown) markdown.hidden = hidden.has(sectionId);
    const signature = scoped.map((item) => {
      const entries = currentDocument.collections?.[`${item.id}.items`]?.custom;
      return `${item.id}:${item.kind}:${Array.isArray(entries) ? entries.filter((entry) => entry && typeof entry.id === 'string').map((entry) => entry.id).join(',') : ''}`;
    }).join('|');
    manifest.fields = sourceFields.slice();
    manifest.sections = sourceSections.slice();
    manifest.collections = [];
    blocksHost.replaceChildren();
    for (const item of ordered) {
      const id = item.id;
      const kind = item.kind;
      const section = document.createElement('section');
      section.className = `cms-doc-block cms-doc-block-${kind}`;
      section.setAttribute('data-cms-section', id);
      section.id = id;
      if (hidden.has(id)) section.hidden = true;
      manifest.sections.push({ id, label: `${kind[0].toUpperCase()}${kind.slice(1)} block`, group: 'page' });
      const addField = (part, label, fieldKind, defaultValue, extra = {}) => manifest.fields.push({ key: `${id}.${part}`, label, kind: fieldKind, sectionId: id, defaultValue, ...extra });
      if (['text', 'split', 'features', 'cta'].includes(kind)) {
        const heading = kind === 'cta' ? 'See Antifailure in action.' : kind === 'features' ? 'From a change to a decision.' : 'Test the change before you ship.';
        const body = { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Give your team a production twin to test the change, inspect the results, and decide what to deploy.' }] }] };
        addField('heading', 'Heading', 'text', heading);
        addField('headingLevel', 'Heading level', 'select', 'h2', { options: ['h1', 'h2', 'h3'].map((value) => ({ value, label: value.toUpperCase() })) });
        addField('body', 'Body', 'richtext', body);
        const level = field(id, 'headingLevel', 'h2');
        section.append(element(['h1', 'h2', 'h3'].includes(level) ? level : 'h2', String(field(id, 'heading', heading)), `${id}.heading`));
        section.append(renderRich(field(id, 'body', body), `${id}.body`));
        if (kind === 'features') {
          const collectionKey = `${id}.items`;
          const defaults = [
            { id: 'rehearse', title: 'Run the change', body: 'Test on an isolated copy of your application.' },
            { id: 'inspect', title: 'Review the findings', body: 'See what changed before it reaches production.' },
            { id: 'decide', title: 'Decide what to ship', body: 'Give your team the evidence to make the call.' },
          ];
          const patch = currentDocument.collections?.[collectionKey];
          const customItems = Array.isArray(patch?.custom) ? patch.custom.filter((entry) => entry && typeof entry.id === 'string') : [];
          const itemRows = [...defaults, ...customItems.map((entry) => ({ ...(entry.fields && typeof entry.fields === 'object' ? entry.fields : {}), id: entry.id }))];
          const hiddenItems = new Set(Array.isArray(patch?.hidden) ? patch.hidden : []);
          for (const move of Array.isArray(patch?.moves) ? patch.moves : []) {
            if (!move || typeof move.id !== 'string') continue;
            const index = itemRows.findIndex((entry) => entry.id === move.id);
            if (index < 0) continue;
            const [row] = itemRows.splice(index, 1);
            const after = itemRows.findIndex((entry) => entry.id === move.after);
            itemRows.splice(move.after === null ? 0 : after < 0 ? itemRows.length : after + 1, 0, row);
          }
          manifest.collections.push({ key: collectionKey, label: 'Features', sectionId: id, items: defaults.map((entry) => ({ id: entry.id, label: entry.title, fields: { title: entry.title, body: entry.body } })) });
          const list = document.createElement('ul');
          for (const entry of itemRows) {
            const fallback = defaults.find((source) => source.id === entry.id);
            const sourceTitle = typeof entry.title === 'string' ? entry.title : fallback?.title || '';
            const sourceBody = typeof entry.body === 'string' ? entry.body : fallback?.body || '';
            manifest.fields.push({ key: `${collectionKey}.${entry.id}.title`, label: `${sourceTitle}: title`, kind: 'text', sectionId: id, defaultValue: sourceTitle });
            manifest.fields.push({ key: `${collectionKey}.${entry.id}.body`, label: `${sourceTitle}: body`, kind: 'text', sectionId: id, defaultValue: sourceBody });
            if (hiddenItems.has(entry.id)) continue;
            const li = document.createElement('li');
            li.append(element('strong', String(currentDocument.fields?.[`${collectionKey}.${entry.id}.title`] ?? sourceTitle), `${collectionKey}.${entry.id}.title`));
            li.append(element('p', String(currentDocument.fields?.[`${collectionKey}.${entry.id}.body`] ?? sourceBody), `${collectionKey}.${entry.id}.body`));
            list.append(li);
          }
          section.append(list);
        }
        if (kind === 'cta') {
          addField('buttonLabel', 'Button label', 'text', 'Request a demo');
          addField('buttonHref', 'Button destination', 'url', '/request-demo');
          const href = field(id, 'buttonHref', '/request-demo');
          const link = element('a', String(field(id, 'buttonLabel', 'Request a demo')), `${id}.buttonLabel`);
          link.href = safeLink(href) ? href : '/request-demo';
          section.append(link);
        }
      }
      if (['image', 'video', 'split'].includes(kind)) {
        addField('media', kind === 'video' ? 'Video' : 'Image', 'media', null);
        addField('caption', 'Media caption', 'text', '');
        const media = mediaFor(id, kind === 'video' ? 'video' : 'image');
        if (media) section.append(media);
        else if (preview) section.append(element('div', 'Choose media in the editor.', `${id}.media`));
        const caption = field(id, 'caption', '');
        if (typeof caption === 'string' && caption) section.append(element('p', caption, `${id}.caption`));
      }
      if (kind === 'shape') {
        addField('shape', 'Shape', 'select', 'circle', { options: ['circle', 'rectangle', 'arch', 'triangle', 'ring'].map((value) => ({ value, label: value })) });
        addField('label', 'Shape caption', 'text', '');
        const shape = element('div', '', `${id}.shape`);
        shape.className = `cms-doc-shape cms-doc-shape-${field(id, 'shape', 'circle')}`;
        section.append(shape);
        const caption = field(id, 'label', '');
        if (caption) section.append(element('p', String(caption), `${id}.label`));
      }
      if (kind === 'divider') {
        addField('variant', 'Divider style', 'select', 'line', { options: ['line', 'dashed', 'dots', 'wave'].map((value) => ({ value, label: value })) });
        addField('label', 'Divider caption', 'text', '');
        const variant = field(id, 'variant', 'line');
        if (variant === 'wave') {
          const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
          svg.setAttribute('viewBox', '0 0 1200 20'); svg.setAttribute('preserveAspectRatio', 'none');
          svg.setAttribute('role', 'separator'); svg.setAttribute('aria-orientation', 'horizontal'); svg.setAttribute('data-cms-key', `${id}.variant`);
          const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
          path.setAttribute('d', `M0 10${'q15 -8 30 0t30 0'.repeat(20)}`); path.setAttribute('fill', 'none'); path.setAttribute('stroke', 'currentColor');
          svg.append(path); section.append(svg);
        } else {
          const line = element('hr', '', `${id}.variant`);
          line.className = `cms-doc-divider-${['dashed', 'dots'].includes(variant) ? variant : 'line'}`;
          section.append(line);
        }
        const caption = field(id, 'label', '');
        if (typeof caption === 'string' && caption) section.append(element('p', caption, `${id}.label`));
      }
      if (kind === 'embed') {
        const defaults = { html: '<h2>Build something useful.</h2>', css: 'body{font:16px/1.6 Arial,sans-serif;padding:24px}', javascript: '' };
        for (const [part, value] of Object.entries(defaults)) addField(part, part.toUpperCase(), 'code', value, { language: part === 'javascript' ? 'javascript' : part });
        addField('title', 'Accessible name', 'text', 'Custom interactive content');
        const frame = document.createElement('iframe');
        frame.title = String(field(id, 'title', 'Custom interactive content'));
        frame.setAttribute('sandbox', 'allow-scripts');
        frame.referrerPolicy = 'no-referrer';
        frame.loading = 'lazy';
        frame.setAttribute('data-cms-key', `${id}.html`);
        frame.srcdoc = embedDocument(String(field(id, 'html', defaults.html)), String(field(id, 'css', defaults.css)), String(field(id, 'javascript', defaults.javascript)));
        section.append(frame);
      }
      blocksHost.append(section);
    }
    if (preview && signature !== blockSignature) post('ready', { manifest });
    blockSignature = signature;
  }
  function apply() {
    const seoTitle = currentDocument.fields?.[`seo.${page}.title`];
    const seoDescription = currentDocument.fields?.[`seo.${page}.description`];
    document.title = typeof seoTitle === 'string' && seoTitle.trim() ? seoTitle : sourceTitle;
    for (const selector of ['meta[name="description"]', 'meta[property="og:description"]', 'meta[name="twitter:description"]']) {
      const tag = document.querySelector(selector);
      if (tag) tag.content = typeof seoDescription === 'string' && seoDescription.trim() ? seoDescription : sourceDescription;
    }
    for (const selector of ['meta[property="og:title"]', 'meta[name="twitter:title"]']) {
      const tag = document.querySelector(selector);
      if (tag) tag.content = document.title;
    }
    for (const [key, original] of nodes) {
      const value = currentDocument?.fields && Object.hasOwn(currentDocument.fields, key) ? currentDocument.fields[key] : original.value;
      const next = `${original.prefix}${typeof value === 'string' && value.length <= 100000 ? value : original.value}${original.suffix}`;
      if (original.node.textContent !== next) original.node.textContent = next;
    }
    for (const [key, original] of images) {
      const selected = currentDocument.fields?.[key];
      if (selected === null) { original.element.hidden = true; original.element.style.setProperty('display', 'none', 'important'); continue; }
      original.element.hidden = original.hidden;
      if (original.display) original.element.style.setProperty('display', original.display, original.displayPriority);
      else original.element.style.removeProperty('display');
      if (!selected || selected.type !== 'media') {
        original.element.src = original.src;
        if (original.srcSet) original.element.setAttribute('srcset', original.srcSet);
        original.element.alt = original.alt;
        continue;
      }
      let src = null;
      if (selected.source === 'asset' && /^[a-f0-9-]{36}$/i.test(selected.assetId || '')) src = assetUrls[selected.assetId] || `https://app.antifailure.dev/v1/website/media/${selected.assetId}`;
      else if (selected.source === 'builtin' && /^\/(?!\/)/.test(selected.src || '')) src = selected.src;
      if (src) {
        try {
          const checked = new URL(src, location.origin);
          if (![location.origin, 'https://app.antifailure.dev', ...(location.hostname === 'localhost' ? ['http://localhost:4332'] : [])].includes(checked.origin)) src = null;
        } catch { src = null; }
      }
      original.element.src = src || original.src;
      if (src && src !== original.src) original.element.removeAttribute('srcset');
      else if (original.srcSet) original.element.setAttribute('srcset', original.srcSet);
      original.element.alt = typeof selected.alt === 'string' ? selected.alt : original.alt;
    }
    for (const [key, original] of chrome) {
      const value = currentDocument.fields && Object.hasOwn(currentDocument.fields, key) ? currentDocument.fields[key] : original.value;
      if (typeof value !== 'string') continue;
      for (const element of original.elements) {
        if (original.kind === 'text' && element.textContent !== value) element.textContent = value;
        else if (original.kind === 'url' && safeLink(value)) element.setAttribute('href', value);
      }
    }
    for (const id of ['header', 'footer']) {
      for (const element of document.querySelectorAll(`[data-cms-section="${id}"]`)) element.hidden = Boolean(currentDocument.sections?.hidden?.includes(id));
    }
    const logo = document.querySelector('.af-wordmark');
    const accessibleLogo = currentDocument.fields?.['header.logo.label'];
    if (logo) logo.setAttribute('aria-label', typeof accessibleLogo === 'string' ? accessibleLogo : 'Antifailure');
    const originalMark = logo?.querySelector('svg');
    const selectedLogo = currentDocument.fields?.['header.logo.image'];
    const existingLogo = logo?.querySelector('[data-cms-logo-image]');
    if (selectedLogo?.type === 'media') {
      let src = null;
      if (selectedLogo.source === 'asset' && /^[a-f0-9-]{36}$/i.test(selectedLogo.assetId || '')) src = assetUrls[selectedLogo.assetId] || `https://app.antifailure.dev/v1/website/media/${selectedLogo.assetId}`;
      else if (selectedLogo.source === 'builtin' && /^\/(?!\/)/.test(selectedLogo.src || '')) src = selectedLogo.src;
      if (src) {
        try {
          const checked = new URL(src, location.origin);
          if (![location.origin, 'https://app.antifailure.dev', ...(location.hostname === 'localhost' ? ['http://localhost:4332'] : [])].includes(checked.origin)) src = null;
        } catch { src = null; }
      }
      if (src && logo) {
        const image = existingLogo || document.createElement('img');
        image.setAttribute('data-cms-logo-image', '');
        image.setAttribute('data-cms-key', 'header.logo.image');
        image.setAttribute('alt', selectedLogo.alt || '');
        image.setAttribute('width', '24'); image.setAttribute('height', '24');
        image.setAttribute('src', src);
        if (!existingLogo) originalMark?.after(image);
        if (originalMark) originalMark.hidden = true;
      }
    } else {
      existingLogo?.remove();
      if (originalMark) originalMark.hidden = false;
    }
    const rules = [];
    const loadedFonts = new Set();
    const properties = {
      fontSize: 'font-size', fontWeight: 'font-weight', lineHeight: 'line-height', letterSpacing: 'letter-spacing',
      color: 'color', backgroundColor: 'background-color', textAlign: 'text-align',
      paddingTop: 'padding-top', paddingRight: 'padding-right', paddingBottom: 'padding-bottom', paddingLeft: 'padding-left',
      marginTop: 'margin-top', marginBottom: 'margin-bottom', gap: 'gap', maxWidth: 'max-width', width: 'width',
      minHeight: 'min-height', borderRadius: 'border-radius', opacity: 'opacity', zIndex: 'z-index', imageFit: 'object-fit',
    };
    const unitless = new Set(['fontWeight', 'lineHeight', 'opacity', 'zIndex']);
    const media = { desktop: '@media(min-width:1024px)', tablet: '@media(min-width:768px) and (max-width:1023px)', mobile: '@media(max-width:767px)' };
    for (const [target, responsive] of Object.entries(currentDocument.styles || {})) {
      if (!/^[a-zA-Z0-9._-]{1,240}$/.test(target) || !responsive || typeof responsive !== 'object') continue;
      const selector = target === 'global' ? ':root' : `[data-cms-key="${CSS.escape(target)}"],[data-cms-section="${CSS.escape(target)}"]`;
      for (const [breakpoint, values] of Object.entries(responsive)) {
        if (!Object.hasOwn(media, breakpoint) || !values || typeof values !== 'object') continue;
        const declarations = [];
        for (const [name, raw] of Object.entries(values)) {
          if (name === 'position' && ['relative', 'absolute', 'fixed'].includes(raw)) { declarations.push(`position:${raw}`); continue; }
          if (name === 'layout' && typeof raw === 'string') {
            if (raw === 'stack') declarations.push('display:flex;flex-direction:column');
            if (raw === 'center') declarations.push('display:grid;place-items:center;text-align:center');
            if (raw === 'media-left' || raw === 'media-right') declarations.push(`display:grid;grid-template-columns:repeat(2,minmax(0,1fr));direction:${raw === 'media-left' ? 'rtl' : 'ltr'}`);
            continue;
          }
          if (name === 'fontFamily' && typeof raw === 'string') {
            const choice = fonts.find((font) => font.key === raw);
            if (choice) declarations.push(`font-family:${choice.family}`);
            else if (/^asset:[a-f0-9-]{36}$/i.test(raw)) {
              const id = raw.slice(6);
              const candidate = assetUrls[id] || `https://app.antifailure.dev/v1/website/media/${id}`;
              try {
                const url = new URL(candidate);
                if ((url.origin === 'https://app.antifailure.dev' || location.hostname === 'localhost' && url.origin === 'http://localhost:4332') && !loadedFonts.has(id)) {
                  rules.push(`@font-face{font-family:"cms-font-${id}";src:url(${JSON.stringify(url.href)}) format("woff2");font-display:swap}`);
                  loadedFonts.add(id);
                }
                if (loadedFonts.has(id)) declarations.push(`font-family:"cms-font-${id}"`);
              } catch { /* Unknown font assets keep the source typeface. */ }
            }
            continue;
          }
          const cssName = properties[name];
          if (!cssName) continue;
          if (typeof raw === 'number' && Number.isFinite(raw)) declarations.push(`${cssName}:${raw}${unitless.has(name) ? '' : 'px'}`);
          else if (typeof raw === 'string' && /^#[a-f0-9]{3,8}$/i.test(raw)) declarations.push(`${cssName}:${raw}`);
          else if ((name === 'textAlign' && ['left', 'center', 'right', 'justify'].includes(raw)) || (name === 'imageFit' && ['cover', 'contain'].includes(raw))) declarations.push(`${cssName}:${raw}`);
        }
        if (typeof values.x === 'number' || typeof values.y === 'number') declarations.push(`transform:translate(${Number(values.x) || 0}px,${Number(values.y) || 0}px)`);
        if (typeof values.focalX === 'number' || typeof values.focalY === 'number') declarations.push(`object-position:${Number(values.focalX) || 50}% ${Number(values.focalY) || 50}%`);
        if (!declarations.length) continue;
        const rule = `${selector}{${declarations.join(';')}}`;
        rules.push(media[breakpoint] ? `${media[breakpoint]}{${rule}}` : rule);
      }
    }
    styleElement.textContent = rules.join('\n');
    renderBlocks();
  }
  function post(type, payload) {
    if (preview) window.parent.postMessage({ protocol: 'antifailure-cms', version: 1, session, type, payload }, parentOrigin);
  }
  if (preview) {
    document.documentElement.classList.add('af-cms-preview');
    document.addEventListener('click', (event) => {
      if (!editing) return;
      const target = event.target instanceof Element ? event.target : null;
      const field = target?.closest('[data-cms-key]');
      if (field) {
        event.preventDefault(); event.stopPropagation();
        const section = field.closest('[data-cms-section]');
        post('select', { key: field.getAttribute('data-cms-key'), sectionId: section?.getAttribute('data-cms-section') || sectionId });
      } else if (target?.closest('[data-cms-section^="custom-"]')) {
        event.preventDefault(); event.stopPropagation();
        post('select', { sectionId: target.closest('[data-cms-section^="custom-"]').getAttribute('data-cms-section') });
      } else if (target?.closest('a,button')) { event.preventDefault(); event.stopPropagation(); }
    }, true);
    document.addEventListener('submit', (event) => { if (editing) event.preventDefault(); }, true);
    window.addEventListener('message', (event) => {
      if (event.origin !== parentOrigin || event.source !== window.parent) return;
      const message = event.data;
      if (!message || message.protocol !== 'antifailure-cms' || message.version !== 1 || message.session !== session) return;
      if (message.type === 'init' || message.type === 'update') {
        currentDocument = message.payload?.document || { fields: {}, styles: {} };
        assetUrls = message.payload?.assetUrls || {};
        editing = message.payload?.mode === 'edit';
        document.documentElement.classList.toggle('af-cms-editing', editing);
        apply();
      } else if (message.type === 'select') {
        const key = message.payload?.key;
        const section = message.payload?.sectionId;
        const node = key ? nodes.get(key)?.node?.parentElement || document.querySelector(`[data-cms-key="${CSS.escape(key)}"]`) :
          section ? document.querySelector(`[data-cms-section="${CSS.escape(section)}"]`) : null;
        node?.scrollIntoView({ block: 'center', behavior: 'instant' });
      }
    });
    post('ready', { manifest });
  } else {
    const refresh = async () => {
      if (document.visibilityState === 'hidden') return;
      try {
        const response = await fetch(PAGE_API, { credentials: 'omit', cache: 'no-cache' });
        if (!response.ok) return;
        const data = await response.json();
        if (!Number.isSafeInteger(data.revision) || data.revision <= revision) return;
        if (!data.document || typeof data.document !== 'object' || Array.isArray(data.document)) return;
        revision = data.revision;
        currentDocument = data.document;
        apply();
      } catch { /* Markdown defaults remain available when the API is offline. */ }
    };
    void refresh();
    setInterval(() => { void refresh(); }, 60_000);
    window.addEventListener('focus', refresh);
  }
})();
