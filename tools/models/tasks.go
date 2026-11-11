package models

import (
	"fmt"
	regel "github.com/sodiqscript111/Regel"
)

func tasks(name string, staleDependencyState, unsafeReopen bool) *regel.Spec {
	s := regel.New(name).
		Var("taskState", regel.Raw(`[t \in 1..3 |-> "not_started"]`)).
		Var("dependencies", regel.Raw(`{}`)).Var("satisfied", regel.Raw(`{}`))
	// We bound paths to three tasks, including length-three paths to detect cycles.
	paths := `{p \in ((1..3) \X (1..3)) : p \in dependencies \/ (\E b \in 1..3 : <<p[1],b>> \in dependencies /\ <<b,p[2]>> \in dependencies) \/ (\E b,c \in 1..3 : <<p[1],b>> \in dependencies /\ <<b,c>> \in dependencies /\ <<c,p[2]>> \in dependencies)}`
	for task := 1; task <= 3; task++ {
		start := fmt.Sprintf(`taskState[%d] = "not_started" /\ \A p \in dependencies : p[1] = %d => p \in satisfied`, task, task)
		label := fmt.Sprintf("start%d", task)
		s.Program(label, "w"+label).Step(label, regel.Set("taskState", guarded(start, fmt.Sprintf(`[taskState EXCEPT ![%d] = "in_progress"]`, task), "taskState")), regel.Goto(label))
		done := fmt.Sprintf(`taskState[%d] = "in_progress"`, task)
		label = fmt.Sprintf("done%d", task)
		ops := []regel.Op{regel.Set("taskState", guarded(done, fmt.Sprintf(`[taskState EXCEPT ![%d] = "done"]`, task), "taskState"))}
		if !staleDependencyState {
			ops = append(ops, regel.Set("satisfied", guarded(done, fmt.Sprintf(`satisfied \cup {p \in dependencies : p[2] = %d}`, task), "satisfied")))
		}
		ops = append(ops, regel.Goto(label))
		s.Program(label, "w"+label).Step(label, ops...)
		reopen := fmt.Sprintf(`taskState[%d] = "done"`, task)
		if !unsafeReopen {
			reopen += fmt.Sprintf(` /\ \A p \in %s : p[2] = %d => taskState[p[1]] = "not_started"`, paths, task)
		}
		label = fmt.Sprintf("reopen%d", task)
		s.Program(label, "w"+label).Step(label,
			regel.Set("taskState", guarded(reopen, fmt.Sprintf(`[taskState EXCEPT ![%d] = "in_progress"]`, task), "taskState")),
			regel.Set("satisfied", guarded(reopen, fmt.Sprintf(`{p \in satisfied : p[2] # %d}`, task), "satisfied")), regel.Goto(label))
		for required := 1; required <= 3; required++ {
			if task == required {
				continue
			}
			pair := fmt.Sprintf("<<%d,%d>>", task, required)
			add := fmt.Sprintf(`%s \notin dependencies /\ <<%d,%d>> \notin %s /\ (taskState[%d] = "not_started" \/ taskState[%d] = "done")`, pair, required, task, paths, task, required)
			label = fmt.Sprintf("add%d%d", task, required)
			s.Program(label, "w"+label).Step(label,
				regel.Set("dependencies", guarded(add, "dependencies \\cup {"+pair+"}", "dependencies")),
				regel.Set("satisfied", guarded(add+fmt.Sprintf(` /\ taskState[%d] = "done"`, required), "satisfied \\cup {"+pair+"}", "satisfied")), regel.Goto(label))
			label = fmt.Sprintf("remove%d%d", task, required)
			s.Program(label, "w"+label).Step(label, regel.Set("dependencies", "dependencies \\ {"+pair+"}"), regel.Set("satisfied", "satisfied \\ {"+pair+"}"), regel.Goto(label))
		}
	}
	s.Always("StoredDependencyState", `satisfied = {p \in dependencies : taskState[p[2]] = "done"}`).
		Always("NoDependencyCycles", `\A t \in 1..3 : <<t,t>> \notin `+paths).
		Always("StartedTasksUnblocked", `\A p \in `+paths+` : taskState[p[1]] \in {"in_progress","done"} => taskState[p[2]] = "done"`)
	return s
}
