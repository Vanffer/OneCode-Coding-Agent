package permission

import "testing"

func TestBlacklist(t *testing.T) {
	blacklist := NewBlacklist()
	commands := []string{
		"rm -rf /",
		"sudo rm -rf /",
		"format c:",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		":(){ :|:& };:",
	}

	for _, command := range commands {
		target := Target{Kind: TargetCommand, Command: command}
		decision, ok := blacklist.Check(target)
		if !ok {
			t.Fatalf("expected blacklist to reject %q", command)
		}
		if decision.Action != ActionDeny || decision.Scope != ScopeBuiltin {
			t.Fatalf("unexpected decision for %q: %+v", command, decision)
		}
	}
}

func TestBlacklistIgnoresNonCommandTarget(t *testing.T) {
	blacklist := NewBlacklist()
	_, ok := blacklist.Check(Target{Kind: TargetPath, Path: "rm -rf /", Value: "rm -rf /"})
	if ok {
		t.Fatal("blacklist should only apply to command targets")
	}
}
