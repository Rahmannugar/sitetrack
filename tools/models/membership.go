package models

import (
	"fmt"
	regel "github.com/sodiqscript111/Regel"
)

func membership(name string, splitCheck bool) *regel.Spec {
	s := regel.New(name).Var("members", regel.Raw(`{1,2,3}`)).Var("admins", regel.Raw(`{1,2}`))
	if splitCheck {
		s.Local("approved", false)
	}
	for actor := 1; actor <= 3; actor++ {
		for target := 1; target <= 3; target++ {
			if actor == target {
				continue
			}
			for _, action := range []string{"promote", "demote", "remove"} {
				label := fmt.Sprintf("%s%d%d", action, actor, target)
				guard := fmt.Sprintf(`%d \in admins /\ %d \in members`, actor, target)
				if action != "promote" {
					guard += fmt.Sprintf(` /\ (%d \notin admins \/ Cardinality(admins) > 1)`, target)
				}
				program := s.Program(label, "w"+label)
				first := label
				if splitCheck && action != "promote" {
					program.Step(label, regel.Set("approved", guard))
					label = "commit" + label
					guard = "approved[self]"
				}
				nextAdmins := fmt.Sprintf(`admins \ {%d}`, target)
				if action == "promote" {
					nextAdmins = fmt.Sprintf(`admins \cup {%d}`, target)
				}
				ops := []regel.Op{regel.Set("admins", guarded(guard, nextAdmins, "admins"))}
				if action == "remove" {
					ops = append(ops, regel.Set("members", guarded(guard, fmt.Sprintf(`members \ {%d}`, target), "members")))
				}
				ops = append(ops, regel.Goto(first))
				program.Step(label, ops...)
			}
		}
	}
	return s.Always("AtLeastOneAdmin", `Cardinality(admins) >= 1`).Always("AdminsAreMembers", `admins \subseteq members`)
}
