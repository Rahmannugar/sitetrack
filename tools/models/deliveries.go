package models

import (
	"fmt"
	regel "github.com/sodiqscript111/Regel"
	"strings"
)

func guarded(condition, next, previous string) string {
	return "IF (" + condition + ") THEN (" + next + ") ELSE " + previous
}
func changeStore(variable, store, delta string) string {
	return fmt.Sprintf(`[%s EXCEPT ![%s] = [m \in 1..2 |-> %s[%s][m] %s]]`, variable, store, variable, store, delta)
}

func deliveries(name string, splitSend, repeatReceive, ignoreTransit bool) *regel.Spec {
	s := regel.New(name).
		Var("stock", regel.Raw(`[s \in {"company","p1","p2"} |-> [m \in 1..2 |-> IF s = "company" THEN 2 ELSE 0]]`)).
		Var("transit", regel.Raw(`[s \in {"company","p1","p2"} |-> [m \in 1..2 |-> 0]]`)).
		Var("deliveryState", regel.Raw(`[d \in 1..3 |-> "new"]`)).
		Var("completions", regel.Raw(`[d \in 1..3 |-> 0]`)).
		Var("added", 0)
	if splitSend {
		s.Local("approved", false)
	}
	sources := []string{`"company"`, `"company"`, `"p1"`}
	destinations := []string{`"p1"`, `"p2"`, `"company"`}
	amounts := []string{`<<2,1>>`, `<<1,2>>`, `<<1,1>>`}
	for i, source := range sources {
		id := i + 1
		dest, amount := destinations[i], amounts[i]
		sendGuard := fmt.Sprintf(`deliveryState[%d] = "new" /\ \A m \in 1..2 : stock[%s][m] >= (%s)[m]`, id, source, amount)
		label := fmt.Sprintf("send%d", id)
		program := s.Program(label, "w"+label)
		if splitSend {
			program.Step(label, regel.Set("approved", sendGuard))
			sendGuard = "approved[self]"
			label = fmt.Sprintf("debit%d", id)
		}
		program.Step(label,
			regel.Set("stock", guarded(sendGuard, changeStore("stock", source, "- ("+amount+")[m]"), "stock")),
			regel.Set("transit", guarded(sendGuard, changeStore("transit", source, "+ ("+amount+")[m]"), "transit")),
			regel.Set("deliveryState", guarded(sendGuard, fmt.Sprintf(`[deliveryState EXCEPT ![%d] = "in_transit"]`, id), "deliveryState")),
			regel.Goto(fmt.Sprintf("send%d", id)))
		for _, action := range []string{"receive", "reject", "cancel"} {
			guard := fmt.Sprintf(`deliveryState[%d] = "in_transit"`, id)
			status, store := "delivered", dest
			if action == "receive" {
				if repeatReceive {
					guard = fmt.Sprintf(`deliveryState[%d] \in {"in_transit","delivered"}`, id)
				}
				guard += fmt.Sprintf(` /\ \A m \in 1..2 : stock[%s][m] + transit[%s][m] + (%s)[m] <= 3`, dest, dest, amount)
			} else {
				status = map[string]string{"reject": "rejected", "cancel": "cancelled"}[action]
				store = source
			}
			label := fmt.Sprintf("%s%d", action, id)
			s.Program(label, "w"+label).Step(label,
				regel.Set("stock", guarded(guard, changeStore("stock", store, "+ ("+amount+")[m]"), "stock")),
				regel.Set("transit", guarded(guard, changeStore("transit", source, "- ("+amount+")[m]"), "transit")),
				regel.Set("deliveryState", guarded(guard, fmt.Sprintf(`[deliveryState EXCEPT ![%d] = "%s"]`, id, status), "deliveryState")),
				regel.Set("completions", guarded(guard, fmt.Sprintf(`[completions EXCEPT ![%d] = @ + 1]`, id), "completions")),
				regel.Goto(label))
		}
	}
	supplierGuard := `added < 2 /\ \A m \in 1..2 : stock["company"][m] + transit["company"][m] + 1 <= 3`
	if ignoreTransit {
		supplierGuard = strings.Replace(supplierGuard, ` + transit["company"][m]`, "", 1)
	}
	s.Program("supplier", "wsupplier").Step("replenish",
		regel.Set("stock", guarded(supplierGuard, changeStore("stock", `"company"`, "+ 1"), "stock")),
		regel.Set("added", guarded(supplierGuard, "added + 1", "added")), regel.Goto("replenish"))
	s.Always("TerminalOnce", `\A d \in 1..3 : completions[d] <= 1`).
		Always("NonnegativeStock", `\A s \in {"company","p1","p2"}, m \in 1..2 : stock[s][m] >= 0 /\ transit[s][m] >= 0`).
		Always("ReturnCapacity", `\A s \in {"company","p1","p2"}, m \in 1..2 : stock[s][m] + transit[s][m] <= 3`).
		Always("Conservation", `\A m \in 1..2 : stock["company"][m] + stock["p1"][m] + stock["p2"][m] + transit["company"][m] + transit["p1"][m] + transit["p2"][m] = 2 + added`)
	pending := make([]string, 3)
	for i, source := range sources {
		pending[i] = fmt.Sprintf(`(IF deliveryState[%d] = "in_transit" /\ s = %s THEN (%s)[m] ELSE 0)`, i+1, source, amounts[i])
	}
	s.Always("TransitMatchesDeliveries", `\A s \in {"company","p1","p2"}, m \in 1..2 : transit[s][m] = `+strings.Join(pending, " + "))
	return s
}
