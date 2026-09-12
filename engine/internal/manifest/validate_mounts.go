package manifest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// mounts checks what a service asks to read at a path inside its container.
//
// The rules here are the ones the published schema cannot express, and every
// one of them refuses a manifest that would otherwise start a container in a
// state its author did not write. That matters more for this key than for most:
// a service whose configuration file did not arrive starts on its image's
// defaults and reports itself running, which is a DIFFERENT TOPOLOGY reported
// as the one the recipe asked for. Both ClickHouse services in the published
// ClickHouse recipe fail exactly that way, and the twin would have said
// nothing.
//
// The containment rule is the one worth reading twice, and it is enforced here
// rather than in the runtime because a refusal at validation names the line in
// the file. A repository path is COPIED into the container. So a path that
// leaves the repository is refused, and a path that leaves it through a
// SYMBOLIC LINK is refused too: `confine` is lexical, and a link called
// `config` pointing at /Users/someone/.ssh satisfies every lexical rule there
// is. Nothing stops somebody committing that link, and the copy would then put
// its target inside a container whose whole purpose is to run code being
// rehearsed.
func (v *validator) mounts(base string, s *schema.Service) {
	at := map[string]int{}
	vols := map[string]int{}
	for i := range s.Mounts {
		m := s.Mounts[i]
		mb := fmt.Sprintf("%s.mounts[%d]", base, i)

		switch {
		case m.Path == "" && m.Volume == "":
			v.add(mb,
				fmt.Sprintf("A mount on service %q names neither a path nor a volume.", s.Name),
				"Give path, for a file or directory copied out of the repository, or volume, "+
					"for a named store the environment keeps across a restart.")
		case m.Path != "" && m.Volume != "":
			v.add(mb,
				fmt.Sprintf("A mount on service %q names both a path and a volume.", s.Name),
				"They are different mechanisms and a mount is one of them. A path is copied in "+
					"and is read only to the service; a volume is writable and survives a restart. "+
					"Use two mounts if the service needs both, at two different places.")
		}

		v.mountTarget(mb, s, m, at, i)
		if m.Path != "" {
			v.mountSource(mb, s, m)
		}
		if m.Volume != "" {
			if prev, dup := vols[m.Volume]; dup {
				v.add(mb+".volume",
					fmt.Sprintf("Service %q mounts the volume %q twice.", s.Name, m.Volume),
					fmt.Sprintf("The first is mounts[%d]. One volume at two paths inside one "+
						"container is two views of the same bytes, and a service that wrote "+
						"through one and read through the other would see its own writes "+
						"appear somewhere it did not put them.", prev))
			}
			vols[m.Volume] = i
		}
	}
}

// mountTarget checks the path inside the container.
func (v *validator) mountTarget(mb string, s *schema.Service, m schema.Mount, at map[string]int, i int) {
	target := strings.TrimSpace(m.At)
	if target == "" {
		v.add(mb+".at",
			fmt.Sprintf("A mount on service %q names no place to put it.", s.Name),
			"Add at, the absolute path inside the container, for example "+
				"/etc/clickhouse-server/config.d/keeper.xml.")
		return
	}
	if !strings.HasPrefix(target, "/") {
		v.add(mb+".at",
			fmt.Sprintf("The mount target %q on service %q is not an absolute path.", m.At, s.Name),
			"A path inside a container is resolved against the image's own root, so it has to "+
				"start with a slash. A relative one would depend on a working directory the "+
				"manifest cannot see.")
		return
	}
	// Cleaned, because /etc/../root and /root are the same place and only one
	// of them reads like it. A target carrying a parent segment is refused
	// rather than silently cleaned, so that what the file says and what the
	// container gets are the same string.
	if clean := path.Clean(target); clean != target {
		v.add(mb+".at",
			fmt.Sprintf("The mount target %q on service %q is not in its simplest form.", m.At, s.Name),
			fmt.Sprintf("It names %s. Write that instead: a target with a parent or a trailing "+
				"segment that goes nowhere is read one way by a person and another by the "+
				"container.", clean))
		return
	}
	if target == "/" {
		v.add(mb+".at",
			fmt.Sprintf("A mount on service %q asks for the container's root.", s.Name),
			"Mounting over / replaces the image, including the command that starts the "+
				"service. Name the directory or the file the service actually reads.")
		return
	}
	if prev, dup := at[target]; dup {
		v.add(mb+".at",
			fmt.Sprintf("Two mounts on service %q both land at %q.", s.Name, target),
			fmt.Sprintf("The first is mounts[%d]. The second would decide what the first "+
				"meant, and which one won would depend on the order they were applied in.", prev))
	}
	at[target] = i
}

// mountSource checks a repository path, including where it leads.
func (v *validator) mountSource(mb string, s *schema.Service, m schema.Mount) {
	clean, ok := confine(m.Path)
	if !ok {
		v.add(mb+".path",
			fmt.Sprintf("The mount source %q on service %q resolves outside the repository.", m.Path, s.Name),
			"Use a path relative to the repository root, with no leading slash and no parent "+
				"segments. A mount reads the repository and nothing else on the machine.")
		return
	}
	if clean == "" {
		v.add(mb+".path",
			fmt.Sprintf("A mount on service %q names the repository root as its source.", s.Name),
			"Name the file or directory the service reads. The whole repository includes .git "+
				"and every dependency directory in it, and copying that into a container is "+
				"minutes of io for bytes nothing reads.")
		return
	}
	if !v.pathExists(clean) {
		v.add(mb+".path",
			fmt.Sprintf("The mount source %q on service %q does not exist.", m.Path, s.Name),
			"Check the spelling. A service whose configuration file did not arrive starts on "+
				"its image's defaults and reports itself running, so this is refused here "+
				"rather than discovered from a container's behaviour.")
		return
	}
	v.mountSourceStaysInside(mb, s, m, clean)
}

// mountSourceStaysInside refuses a source that leaves the repository through a
// symbolic link.
//
// Separate from the lexical check above because it is a different question and
// only one of the two can be answered without a filesystem. `confine` reads
// the string; this reads where the string LEADS. A committed link named
// `config` whose target is an absolute path elsewhere on the machine passes
// every lexical rule, and the copy would put that target inside a container
// running code under rehearsal.
//
// Silent when there is no root, which is every caller that validates a
// manifest it was handed rather than one it read off a disk. That is the same
// answer pathExists gives, and for the same reason: a check that cannot look
// must not pretend it looked and must not refuse what it could not read.
func (v *validator) mountSourceStaysInside(mb string, s *schema.Service, m schema.Mount, clean string) {
	if v.root == "" {
		return
	}
	root, err := filepath.EvalSymlinks(v.root)
	if err != nil {
		return
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		v.add(mb+".path",
			fmt.Sprintf("The mount source %q on service %q is a link that leads outside the repository.", m.Path, s.Name),
			"A mount copies what it names into a container that runs the code being "+
				"rehearsed, so following a link out of the tree would hand that container a "+
				"file from the machine. Point it at something committed.")
	}
}

// readinessCheck refuses two answers to one question, and a check on a service
// nothing waits for.
//
// health_path and health_command are both statements about when a service is
// ready, and a service carrying both would have whichever one the runtime
// happened to prefer. That preference is exactly the kind of fact that differs
// between two runtimes and is discovered from a stack that came up on a laptop
// and not on a cluster.
//
// A cron service is refused a command for the reason it is refused a readiness
// wait at all: it is invoked on a schedule rather than run continuously, both
// runtimes report it ready the moment it is scheduled, and a check written on
// one would be a key that is read by nothing. A manifest key that does nothing
// is the defect this repository keeps finding in itself, so it is refused in
// the file rather than ignored in the runtime.
func (v *validator) readinessCheck(base string, s *schema.Service) {
	if s.HealthCommand == "" {
		return
	}
	// What the author WROTE, not what the field holds. The normalizer fills in
	// a default path for a web service, and testing the field would refuse
	// every web service that declared a command.
	if declaredAt(v.doc, base+".health_path") {
		v.add(base+".health_command",
			fmt.Sprintf("Service %q declares both a health path and a health command.", s.Name),
			"They are two answers to when the service is ready. Keep the command if the "+
				"service can only prove it from inside, as pg_isready does for a Postgres "+
				"that accepts connections while it is still running its init scripts, and "+
				"the path otherwise.")
	}
	if s.Kind == schema.ServiceCron {
		v.add(base+".health_command",
			fmt.Sprintf("Service %q is a cron service and declares a health command.", s.Name),
			"A cron service is invoked on a schedule rather than run continuously, so nothing "+
				"waits for it to become ready and the command would never be run.")
	}
}
