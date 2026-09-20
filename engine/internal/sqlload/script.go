package sqlload

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// The declared workload document.
//
// WHY A DOCUMENT RATHER THAN SQL IN THE MANIFEST. A workload is SQL, and SQL
// in a YAML scalar inside the manifest would be SQL nobody's editor
// highlights, nobody's formatter touches and nobody's reviewer reads. It also
// belongs beside the schema rather than beside the environment settings. So
// load.sql.script names a file, exactly as load.scenarios names a scenario
// document, and the manifest keeps the knobs.
//
// WHY THE PARAMETERS ARE DECLARED RATHER THAN INLINE. The alternative is a
// mini language inside the statement, which is what pgbench does with
// :variable and what every tool that does it regrets: the statement stops
// being SQL, so it cannot be pasted into psql, and the substitution is textual
// so a value can become syntax. Here the statement is exactly the SQL the
// server receives, the parameters are $1 upward, and the values are bound by
// the driver. A value can never become syntax, and the statement in the
// document is the statement you can run by hand.
//
// WHY A QUERY PARAMETER EXISTS. It is the difference between a benchmark and a
// rehearsal. An id drawn from the table is an id that exists, so the statement
// reads a row rather than proving that an empty result is fast. The query runs
// once when the run starts, on one connection, and every client draws from the
// same pool, so the seed alone decides which value each client picks.

// ParseScript reads a declared workload document.
//
// Strict, the way the manifest parser is strict: an unknown key is an error
// rather than something dropped. A misspelled weight that silently became zero
// would produce a run whose report says it executed a transaction it never
// picked.
func ParseScript(data []byte) (*Mix, string, error) {
	var doc scriptDoc
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, "", fmt.Errorf("the document could not be read: %w", err)
	}

	name := strings.TrimSpace(doc.Name)
	if name == "" {
		return nil, "", fmt.Errorf("the document does not name itself; add a sql_workload key")
	}
	if len(doc.Transactions) == 0 {
		return nil, "", fmt.Errorf("the workload %q declares no transactions", name)
	}

	mix := &Mix{Source: SourceDeclared, Refused: []Refused{}}
	for i, td := range doc.Transactions {
		tx, err := td.into(i)
		if err != nil {
			return nil, "", err
		}
		mix.Transactions = append(mix.Transactions, tx)
	}
	if err := mix.validate(); err != nil {
		return nil, "", err
	}
	return mix, doc.Description, nil
}

// scriptDoc is the document on disk.
type scriptDoc struct {
	// Name is what the workload is called, and is the key the document is
	// recognised by. A file that is not one of these fails to decode rather
	// than producing an empty workload.
	Name        string `yaml:"sql_workload"`
	Description string `yaml:"description,omitempty"`
	// Transactions are the units of work.
	Transactions []transactionDoc `yaml:"transactions"`
}

type transactionDoc struct {
	Name string `yaml:"transaction"`
	// Weight is how often this transaction is picked relative to the others.
	// Absent means one, so a document that gives no weights runs its
	// transactions equally rather than not at all.
	Weight     *float64       `yaml:"weight,omitempty"`
	Statements []statementDoc `yaml:"statements"`
}

type statementDoc struct {
	// Label names the statement in a report. Absent means the statement's own
	// first words, so a short document needs no labels and a long one can
	// have them.
	Label  string     `yaml:"label,omitempty"`
	SQL    string     `yaml:"sql"`
	Params []paramDoc `yaml:"params,omitempty"`
}

// paramDoc is one parameter. Exactly one of the three is set.
type paramDoc struct {
	Int  *intParamDoc  `yaml:"int,omitempty"`
	Text *textParamDoc `yaml:"text,omitempty"`
	// Query's first column becomes the pool this parameter draws from.
	Query string `yaml:"query,omitempty"`
}

type intParamDoc struct {
	Min int64 `yaml:"min"`
	Max int64 `yaml:"max"`
}

type textParamDoc struct {
	Values []string `yaml:"values"`
}

func (t transactionDoc) into(position int) (Transaction, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return Transaction{}, fmt.Errorf("transaction %d does not name itself; add a transaction key", position+1)
	}
	weight := 1.0
	if t.Weight != nil {
		weight = *t.Weight
	}
	if weight < 0 {
		return Transaction{}, fmt.Errorf("the transaction %q has a negative weight", name)
	}
	if len(t.Statements) == 0 {
		return Transaction{}, fmt.Errorf("the transaction %q declares no statements", name)
	}

	tx := Transaction{Name: name, Weight: weight}
	labels := map[string]bool{}
	for i, sd := range t.Statements {
		st, err := sd.into(name, i, labels)
		if err != nil {
			return Transaction{}, err
		}
		tx.Statements = append(tx.Statements, st)
	}
	return tx, nil
}

func (s statementDoc) into(tx string, position int, labels map[string]bool) (Statement, error) {
	sql := strings.TrimSpace(s.SQL)
	if sql == "" {
		return Statement{}, fmt.Errorf("statement %d of %q carries no sql", position+1, tx)
	}
	name := strings.TrimSpace(s.Label)
	if name == "" {
		name = label(sql)
	}
	if labels[name] {
		// Two statements with one label inside one transaction would share a
		// row in the report, so their latencies would be averaged together
		// and neither number would be either statement's.
		return Statement{}, fmt.Errorf("two statements of %q are both labelled %q", tx, name)
	}
	labels[name] = true

	st := Statement{Label: name, SQL: sql, Write: classifyStatement(sql) != statementRead}
	for i, pd := range s.Params {
		p, err := pd.into(tx, name, i)
		if err != nil {
			return Statement{}, err
		}
		st.Params = append(st.Params, p)
	}
	return st, nil
}

func (p paramDoc) into(tx, label string, position int) (Param, error) {
	set := 0
	if p.Int != nil {
		set++
	}
	if p.Text != nil {
		set++
	}
	if strings.TrimSpace(p.Query) != "" {
		set++
	}
	if set != 1 {
		return Param{}, fmt.Errorf(
			"parameter %d of %q in %q has to set exactly one of int, text or query, and sets %d",
			position+1, label, tx, set)
	}
	switch {
	case p.Int != nil:
		if p.Int.Max < p.Int.Min {
			return Param{}, fmt.Errorf("parameter %d of %q in %q has a max below its min",
				position+1, label, tx)
		}
		return Param{Kind: ParamInt, Min: p.Int.Min, Max: p.Int.Max}, nil
	case p.Text != nil:
		if len(p.Text.Values) == 0 {
			return Param{}, fmt.Errorf("parameter %d of %q in %q is a text parameter with no values",
				position+1, label, tx)
		}
		return Param{Kind: ParamText, Values: p.Text.Values}, nil
	default:
		return Param{Kind: ParamQuery, Query: strings.TrimSpace(p.Query)}, nil
	}
}
