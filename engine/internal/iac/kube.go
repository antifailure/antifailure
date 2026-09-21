package iac

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The reader for raw Kubernetes manifests.
//
// It works on the YAML NODE TREE rather than on decoded structs, for two
// reasons that are both about honesty. A node carries its own line and column,
// so every field this reader reports can say where it came from, which is half
// of what makes an unmeasured value actionable. And a node tree accepts any
// apiVersion, including a custom resource nobody has taught this reader about,
// where a typed decode would refuse the document or silently drop the fields
// it did not recognise.
//
// ONE BAD DOCUMENT MUST NOT BLANK THE FILE. A Kubernetes file is a stream of
// documents separated by `---`, and a single malformed one is common: a
// templating accident, a stray tab, a document that is a comment and nothing
// else. Each document is decoded and handled ON ITS OWN, and a failure is
// recorded against that document with its index and its line while every other
// document in the file is still read. An all or nothing decode of a stream of
// external documents is how one surprising entry blanks a whole feature, which
// is a failure this repository has already paid for once on a decode boundary.

func readKube(c *candidate, out *Reading) {
	docs, failures := yamlDocuments(c.rel, c.body)
	read := false
	for _, doc := range docs {
		if readKubeDoc(c.rel, doc, out) {
			read = true
		}
	}
	if len(failures) > 0 {
		out.Sources = append(out.Sources, Source{
			Path: c.rel, Dialect: DialectKubernetes, Read: read,
			Why: strings.Join(failures, "; "),
		})
		return
	}
	out.Sources = append(out.Sources, Source{
		Path: c.rel, Dialect: DialectKubernetes, Read: read,
		Why: whenEmpty(read, "no document in this file declares a kind this reader describes"),
	})
}

// yamlDocuments splits a YAML stream into documents, keeping the ones that
// decode and naming the ones that do not.
func yamlDocuments(file string, body []byte) ([]*yaml.Node, []string) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	var (
		docs     []*yaml.Node
		failures []string
	)
	for i := 0; ; i++ {
		var node yaml.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			failures = append(failures, "document "+itoa(i+1)+" did not parse as YAML: "+err.Error())
			// A decoder that has hit a syntax error cannot reliably find the
			// start of the next document, so the rest of the stream is
			// reported as unread rather than guessed at.
			if len(docs) == 0 {
				failures = []string{"the file did not parse as YAML: " + err.Error()}
			} else {
				failures = append(failures, "the documents after it were not read, because a YAML "+
					"decoder cannot find the start of the next document after a syntax error")
			}
			break
		}
		if node.Kind == 0 || len(node.Content) == 0 {
			continue // an empty document, which is legal and says nothing
		}
		docs = append(docs, node.Content[0])
	}
	_ = file
	return docs, failures
}

func readKubeDoc(file string, doc *yaml.Node, out *Reading) bool {
	m := nodeMap(doc)
	kind, _ := nodeStr(m["kind"])
	meta := nodeMap(m["metadata"])
	name, _ := nodeStr(meta["name"])
	namespace, _ := nodeStr(meta["namespace"])
	if namespace == "" {
		namespace = "default"
	}
	if kind == "" || name == "" {
		return false
	}
	api, _ := nodeStr(m["apiVersion"])
	at := posOf(file, doc)
	address := kind + " " + namespace + "/" + name

	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod", "Job", "CronJob":
		out.Components = append(out.Components, kubeWorkload(file, api, kind, name, address, at, m))
		return true
	case "Secret":
		// The name and nothing else. A Kubernetes Secret's `data` is the
		// secret, base64 is not encryption, and the keys alone say what the
		// workload is given without saying what it is.
		c := Component{Name: name, Kind: KindSecret, Address: address, Type: kind, At: at,
			From: DialectKubernetes, Present: Resolved(true, at)}
		for _, key := range sortedKeys(nodeMap(m["data"]), nodeMap(m["stringData"])) {
			c.Attrs = append(c.Attrs, Attr{
				Name:  "data." + key,
				Value: Refused[string](droppedByType, at),
				At:    at,
			})
		}
		out.Components = append(out.Components, c)
		return true
	case "ConfigMap":
		c := Component{Name: name, Kind: KindParameter, Address: address, Type: kind, At: at,
			From: DialectKubernetes, Present: Resolved(true, at)}
		data := nodeMap(m["data"])
		for _, key := range sortedKeys(data) {
			v, _ := nodeStr(data[key])
			c.Attrs = append(c.Attrs, Attr{
				Name:  "data." + key,
				Value: scrub(kind, key, Resolved(v, posOf(file, data[key]))),
				At:    posOf(file, data[key]),
			})
		}
		out.Components = append(out.Components, c)
		return true
	case "NetworkPolicy":
		out.Network = append(out.Network, kubeNetworkPolicy(file, address, at, m)...)
		return true
	}
	return false
}

// kubeWorkload reads a pod carrying kind into a service component.
func kubeWorkload(file, api, kind, name, address string, at Position, m map[string]*yaml.Node) Component {
	c := Component{
		Name: name, Kind: KindService, Address: address, Type: api + " " + kind, At: at,
		From: DialectKubernetes, Present: Resolved(true, at),
	}
	spec := nodeMap(m["spec"])
	if r, ok := spec["replicas"]; ok {
		c.Replicas = nodeInt(file, r)
	} else if kind == "Deployment" || kind == "StatefulSet" || kind == "ReplicaSet" {
		// Kubernetes defaults these to one, and the default is a fact about
		// production rather than a missing value, so it is Known and points at
		// the spec that omitted it.
		c.Replicas = Resolved(1, at)
	}

	podSpec := podSpecOf(spec, kind)
	containers := nodeSeq(podSpec["containers"])
	if len(containers) == 0 {
		return c
	}
	main := nodeMap(containers[0])
	if img, ok := main["image"]; ok {
		s, _ := nodeStr(img)
		c.Image = Resolved(s, posOf(file, img))
	}
	limits := nodeMap(nodeMap(main["resources"])["limits"])
	requests := nodeMap(nodeMap(main["resources"])["requests"])
	c.CPU = firstScalar(file, limits["cpu"], requests["cpu"])
	c.Memory = firstScalar(file, limits["memory"], requests["memory"])

	for _, env := range nodeSeq(main["env"]) {
		e := nodeMap(env)
		n, ok := nodeStr(e["name"])
		if !ok || n == "" {
			continue
		}
		pos := posOf(file, env)
		v := EnvVar{Name: n, At: pos, Present: Resolved(true, pos)}
		// valueFrom.secretKeyRef is the reference, and it is carried. `value`
		// is the value, and it is not: this is the single commonest place a
		// production password sits in a Kubernetes manifest.
		if ref := nodeMap(nodeMap(e["valueFrom"])["secretKeyRef"]); len(ref) > 0 {
			sn, _ := nodeStr(ref["name"])
			sk, _ := nodeStr(ref["key"])
			v.From = Resolved(sn+"/"+sk, pos)
		}
		c.Env = append(c.Env, v)
	}
	for _, from := range nodeSeq(main["envFrom"]) {
		f := nodeMap(from)
		pos := posOf(file, from)
		for _, src := range []string{"secretRef", "configMapRef"} {
			ref := nodeMap(f[src])
			if len(ref) == 0 {
				continue
			}
			rn, _ := nodeStr(ref["name"])
			// envFrom imports EVERY key of a secret or config map as a
			// variable, and which keys those are is not in this file. The
			// names are therefore unreadable rather than absent, which is the
			// difference between "this workload has no other variables" and
			// "this workload has variables I cannot name".
			c.Env = append(c.Env, EnvVar{
				Name: "every key of " + src + " " + rn,
				At:   pos,
				Present: Unresolved[bool]("this workload imports every key of "+rn+" as an "+
					"environment variable, and which keys those are is declared in that object "+
					"rather than here", pos),
			})
		}
	}
	for _, probeName := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
		p, ok := main[probeName]
		if !ok {
			continue
		}
		c.Probes = append(c.Probes, kubeProbe(file, strings.TrimSuffix(probeName, "Probe"), p))
	}
	sortEnv(&c)
	sort.SliceStable(c.Attrs, func(i, j int) bool { return c.Attrs[i].Name < c.Attrs[j].Name })
	return c
}

// podSpecOf digs out the pod spec, which sits at a different depth for each
// workload kind.
func podSpecOf(spec map[string]*yaml.Node, kind string) map[string]*yaml.Node {
	switch kind {
	case "Pod":
		return spec
	case "CronJob":
		return nodeMap(nodeMap(nodeMap(nodeMap(spec["jobTemplate"])["spec"])["template"])["spec"])
	default:
		return nodeMap(nodeMap(spec["template"])["spec"])
	}
}

func kubeProbe(file, typ string, node *yaml.Node) Probe {
	at := posOf(file, node)
	p := Probe{Type: typ, At: at}
	m := nodeMap(node)
	if http := nodeMap(m["httpGet"]); len(http) > 0 {
		p.Path = firstScalar(file, http["path"])
		p.Port = firstScalar(file, http["port"])
		p.Scheme = firstScalar(file, http["scheme"])
		if p.Scheme.State() == Absent {
			p.Scheme = Resolved("HTTP", at)
		}
	} else if tcp := nodeMap(m["tcpSocket"]); len(tcp) > 0 {
		p.Port = firstScalar(file, tcp["port"])
		p.Scheme = Resolved("TCP", at)
	} else if _, ok := m["exec"]; ok {
		p.Scheme = Resolved("exec", at)
	}
	p.TimeoutSeconds = nodeInt(file, m["timeoutSeconds"])
	p.PeriodSeconds = nodeInt(file, m["periodSeconds"])
	p.InitialDelaySeconds = nodeInt(file, m["initialDelaySeconds"])
	p.FailureThreshold = nodeInt(file, m["failureThreshold"])
	return p
}

// kubeNetworkPolicy reads a network policy's rules.
//
// A network policy has no deny rules: it allows, and everything a selected pod
// is not allowed is denied by the policy existing at all. So every rule here
// is an allow, and a policy with an empty egress list is the strongest
// statement in the file, which is why an empty list still produces a rule
// saying so rather than nothing.
func kubeNetworkPolicy(file, address string, at Position, m map[string]*yaml.Node) []NetworkRule {
	spec := nodeMap(m["spec"])
	var out []NetworkRule
	for _, dir := range []struct {
		key string
		d   Direction
	}{{"egress", Egress}, {"ingress", Ingress}} {
		node, declared := spec[dir.key]
		if !declared {
			continue
		}
		rules := nodeSeq(node)
		if len(rules) == 0 {
			out = append(out, NetworkRule{
				Address:   address + "." + dir.key,
				Direction: dir.d,
				Allow:     false,
				To:        Resolved("nothing: the policy declares an empty "+dir.key+" list, which denies all of it", posOf(file, node)),
				At:        at,
			})
			continue
		}
		for i, rule := range rules {
			r := nodeMap(rule)
			pos := posOf(file, rule)
			nr := NetworkRule{
				Address:   address + "." + dir.key + "[" + itoa(i) + "]",
				Direction: dir.d,
				Allow:     true,
				At:        pos,
			}
			var ports, protos []string
			for _, p := range nodeSeq(r["ports"]) {
				pm := nodeMap(p)
				if s, ok := nodeStr(pm["port"]); ok {
					ports = append(ports, s)
				}
				if s, ok := nodeStr(pm["protocol"]); ok {
					protos = append(protos, s)
				}
			}
			if len(ports) > 0 {
				nr.Ports = Resolved(strings.Join(ports, ","), pos)
			}
			if len(protos) > 0 {
				nr.Protocol = Resolved(strings.Join(protos, ","), pos)
			}
			peerKey := "to"
			if dir.d == Ingress {
				peerKey = "from"
			}
			var peers []string
			for _, peer := range nodeSeq(r[peerKey]) {
				pm := nodeMap(peer)
				if ip := nodeMap(pm["ipBlock"]); len(ip) > 0 {
					if cidr, ok := nodeStr(ip["cidr"]); ok {
						peers = append(peers, cidr)
					}
				}
				if _, ok := pm["namespaceSelector"]; ok {
					peers = append(peers, "a namespace selector")
				}
				if _, ok := pm["podSelector"]; ok {
					peers = append(peers, "a pod selector")
				}
			}
			switch {
			case len(peers) > 0:
				nr.To = Resolved(strings.Join(peers, ", "), pos)
			default:
				// No peer list means every destination, which is the most
				// permissive thing a rule can say and must never read as
				// absent.
				nr.To = Resolved("anywhere: the rule names no peer, so it allows all of them", pos)
			}
			out = append(out, nr)
		}
	}
	return out
}

// The small node helpers. Each one answers "not there" rather than panicking,
// because a manifest is external data and a missing key is ordinary.

func nodeMap(n *yaml.Node) map[string]*yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]*yaml.Node, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1]
	}
	return out
}

func nodeSeq(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

func nodeStr(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

func nodeInt(file string, n *yaml.Node) Value[int] {
	if n == nil || n.Kind != yaml.ScalarNode {
		return Value[int]{}
	}
	v, err := strconv.Atoi(strings.TrimSpace(n.Value))
	if err != nil {
		return Unresolved[int]("the value is not a whole number", posOf(file, n))
	}
	return Resolved(v, posOf(file, n))
}

func firstScalar(file string, nodes ...*yaml.Node) Value[string] {
	for _, n := range nodes {
		if s, ok := nodeStr(n); ok {
			return Resolved(s, posOf(file, n))
		}
	}
	return Value[string]{}
}

func posOf(file string, n *yaml.Node) Position {
	if n == nil {
		return Position{File: file}
	}
	return Position{File: file, Line: n.Line, Col: n.Column}
}

func sortedKeys(ms ...map[string]*yaml.Node) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range ms {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
