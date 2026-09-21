package iac

// The structure half of the HCL reader: blocks and attributes.
//
// It does NOT build an expression tree. An attribute's value is kept as the
// run of tokens between the `=` and the line break that ends it, and hcleval.go
// decides what that run means. Keeping them apart is deliberate: the structure
// is what this reader must get exactly right, because getting it wrong
// attributes one resource's attributes to another, and an expression it gets
// wrong merely becomes unreadable, which is an answer rather than a lie.

// hclBody is a block body: its attributes and the blocks nested in it.
type hclBody struct {
	attrs  []hclAttr
	blocks []hclBlock
}

type hclAttr struct {
	name string
	expr []token
	pos  Position
}

type hclBlock struct {
	typ    string
	labels []string
	body   hclBody
	pos    Position
	// condWhy is set when this block came out of a `dynamic` block whose
	// for_each this reader could not resolve, and it says so. Everything the
	// block declares is then DECLARED without being certainly present, which
	// is a third answer the reader would otherwise have to round to one of the
	// other two.
	condWhy string
	// iterator is the name the dynamic block's `for_each` element is bound
	// to inside the generated body: the block's label, or whatever an
	// `iterator` argument renamed it to.
	iterator string
}

// attr finds an attribute by name, and says whether it is there at all, which
// is the difference between Absent and everything else.
func (b hclBody) attr(name string) (hclAttr, bool) {
	for _, a := range b.attrs {
		if a.name == name {
			return a, true
		}
	}
	return hclAttr{}, false
}

// blocksOf returns every nested block of one type, in source order.
//
// A `dynamic "x"` block counts as a block of type x, because that is what it
// generates. This is not a convenience: a reader that did not expand dynamic
// blocks would silently miss most of the environment variables and most of the
// secret references in any configuration written by somebody competent, since
// a dynamic block is how a configuration says "this one only in production".
// What it cannot know is HOW MANY the block generates, so the expansion yields
// ONE block carrying the reason its presence is uncertain.
func (b hclBody) blocksOf(typ string) []hclBlock {
	var out []hclBlock
	for _, blk := range b.blocks {
		switch {
		case blk.typ == typ:
			out = append(out, blk)
		case blk.typ == "dynamic" && len(blk.labels) == 1 && blk.labels[0] == typ:
			if gen, ok := blk.generated(); ok {
				out = append(out, gen)
			}
		}
	}
	return out
}

// generated turns a `dynamic "x" { for_each = ... content { ... } }` into the
// block it stands for.
//
// The for_each is deliberately NOT evaluated. Even when it resolves, knowing
// the collection does not tell this reader what each generated block contains,
// because the content body refers to `x.key` and `x.value` per element. So the
// honest answer is one block, with everything in it declared, and a reason
// saying its presence is decided when Terraform runs.
func (b hclBlock) generated() (hclBlock, bool) {
	// Both halves of this guard are load bearing. Without the type check, any
	// block that happens to nest a block called `content` is read as dynamic
	// and reported under the wrong name. Without the label check, a malformed
	// `dynamic { }` with no label indexes labels[0] and takes the process
	// down, and a reader of other people's infrastructure meets malformed
	// input as a matter of course.
	if b.typ != "dynamic" || len(b.labels) != 1 {
		return hclBlock{}, false
	}
	content, ok := b.body.firstBlock("content")
	if !ok {
		return hclBlock{}, false
	}
	// The iterator defaults to the block's own label and is renamed by an
	// `iterator` argument. Knowing its name is what lets the resolver say
	// "this comes from the dynamic block's iterator" rather than the useless
	// "env.value is a reference this reader cannot resolve", which reads as a
	// gap in the parser instead of as a fact about the configuration. On this
	// repository's own control plane module that one sentence accounted for
	// twenty one of the unmeasured items.
	iterator := b.labels[0]
	if a, ok := b.body.attr("iterator"); ok && len(a.expr) == 1 && a.expr[0].kind == tokIdent {
		iterator = a.expr[0].text
	}
	return hclBlock{
		typ:      b.labels[0],
		body:     content.body,
		pos:      b.pos,
		iterator: iterator,
		condWhy: "this is declared inside a dynamic block, so how many of it there are, and " +
			"whether there are any, is decided when Terraform runs",
	}, true
}

// firstBlock returns the first nested block of one type.
func (b hclBody) firstBlock(typ string) (hclBlock, bool) {
	for _, blk := range b.blocks {
		if blk.typ == typ {
			return blk, true
		}
	}
	return hclBlock{}, false
}

type parser struct {
	toks []token
	i    int
}

// parseFile turns a file's tokens into its top level body, or refuses the
// file.
func parseFile(toks []token) (hclBody, *lexError) {
	p := &parser{toks: toks}
	body, err := p.body(false)
	if err != nil {
		return hclBody{}, err
	}
	if p.peek().kind != tokEOF {
		return hclBody{}, lexErrf(p.peek().pos, "a closing brace with no block open")
	}
	return body, nil
}

func (p *parser) peek() token {
	if p.i >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[p.i]
}

func (p *parser) next() token {
	t := p.peek()
	if p.i < len(p.toks) {
		p.i++
	}
	return t
}

func (p *parser) skipNewlines() {
	for p.peek().kind == tokNewline {
		p.i++
	}
}

// body reads items until end of file, or until the brace that closes a block.
func (p *parser) body(nested bool) (hclBody, *lexError) {
	var out hclBody
	for {
		p.skipNewlines()
		t := p.peek()
		if t.kind == tokEOF {
			if nested {
				return hclBody{}, lexErrf(t.pos, "a block was opened and never closed")
			}
			return out, nil
		}
		if t.kind == tokPunct && t.text == "}" {
			if !nested {
				return hclBody{}, lexErrf(t.pos, "a closing brace with no block open")
			}
			return out, nil
		}
		if t.kind != tokIdent {
			return hclBody{}, lexErrf(t.pos, "%s where a name was expected", t.kind)
		}
		name := p.next()
		switch nxt := p.peek(); {
		case nxt.kind == tokPunct && nxt.text == "=":
			p.next()
			expr, err := p.expr()
			if err != nil {
				return hclBody{}, err
			}
			if len(expr) == 0 {
				return hclBody{}, lexErrf(name.pos, "the attribute %s has nothing after its =", name.text)
			}
			out.attrs = append(out.attrs, hclAttr{name: name.text, expr: expr, pos: name.pos})
		case nxt.kind == tokPunct && nxt.text == "{",
			nxt.kind == tokString, nxt.kind == tokIdent:
			blk, err := p.block(name)
			if err != nil {
				return hclBody{}, err
			}
			out.blocks = append(out.blocks, blk)
		default:
			return hclBody{}, lexErrf(nxt.pos, "%s after the name %s, where this reader expects "+
				"an = or a block", nxt.kind, name.text)
		}
	}
}

// block reads a block's labels and its body.
func (p *parser) block(name token) (hclBlock, *lexError) {
	blk := hclBlock{typ: name.text, pos: name.pos}
	for {
		t := p.peek()
		switch {
		case t.kind == tokString:
			p.next()
			lit, ok := literalOf(t)
			if !ok {
				return hclBlock{}, lexErrf(t.pos, "a block label with an interpolation in it, "+
					"which would make the block's identity depend on a value")
			}
			blk.labels = append(blk.labels, lit)
		case t.kind == tokIdent:
			p.next()
			blk.labels = append(blk.labels, t.text)
		case t.kind == tokPunct && t.text == "{":
			p.next()
			body, err := p.body(true)
			if err != nil {
				return hclBlock{}, err
			}
			closing := p.peek()
			if closing.kind != tokPunct || closing.text != "}" {
				return hclBlock{}, lexErrf(closing.pos, "a block was opened and never closed")
			}
			p.next()
			blk.body = body
			return blk, nil
		default:
			return hclBlock{}, lexErrf(t.pos, "%s in a block header", t.kind)
		}
	}
}

// expr collects the tokens of one attribute's value.
//
// An attribute ends at a line break that is not inside brackets. The closing
// brace of the enclosing block ends one too, which is what makes the one line
// form `locals { a = 1 }` read correctly: a `}` at bracket depth zero cannot
// belong to this expression, because an object constructor inside it would
// have opened a brace first and raised the depth.
func (p *parser) expr() ([]token, *lexError) {
	var out []token
	depth := 0
	for {
		t := p.peek()
		switch {
		case t.kind == tokEOF:
			if depth > 0 {
				return nil, lexErrf(t.pos, "a bracket was opened and never closed")
			}
			return out, nil
		case t.kind == tokNewline:
			if depth == 0 {
				p.next()
				return out, nil
			}
			// A line break inside brackets is KEPT, and dropping it was a bug
			// the tests caught. HCL lets an object be written over lines with
			// no commas at all, which is how almost every real `tags` block is
			// written:
			//
			//	tags = {
			//	  env    = "production"
			//	  region = "eu-west-2"
			//	}
			//
			// With the line breaks thrown away that is one run of tokens with
			// no separator in it, and the object reader either produced one
			// nonsense entry or gave up on the whole object. The line break IS
			// the separator there. It is layout inside `[` and `(`, and the
			// readers that walk those trim it.
		case t.kind == tokPunct && (t.text == "{" || t.text == "[" || t.text == "("):
			depth++
		case t.kind == tokPunct && (t.text == "}" || t.text == "]" || t.text == ")"):
			if depth == 0 {
				if t.text == "}" {
					return out, nil
				}
				return nil, lexErrf(t.pos, "a closing %s with nothing open", t.text)
			}
			depth--
		}
		out = append(out, p.next())
	}
}

// literalOf returns a string token's text when every part of it is literal.
func literalOf(t token) (string, bool) {
	if t.kind != tokString && t.kind != tokHeredoc {
		return "", false
	}
	var sb []byte
	for _, part := range t.parts {
		if part.interp != nil || part.directive {
			return "", false
		}
		sb = append(sb, part.lit...)
	}
	return string(sb), true
}
