package seed

import (
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Profile struct {
	Name          string `json:"name"`
	Users         int
	Projects      int
	Tasks         int
	Dependencies  int
	Materials     int
	Categories    int
	Invitations   int
	Deliveries    int
	DeliveryItems int
}

func ProfileByName(name string) (Profile, error) {
	switch name {
	case "full":
		return Profile{Name: name, Users: 5000, Projects: 2000, Tasks: 500000, Dependencies: 1000000, Materials: 20000, Categories: 40, Invitations: 50000, Deliveries: 400000, DeliveryItems: 3000000}, nil
	case "small":
		return Profile{Name: name, Users: 100, Projects: 4, Tasks: 200, Dependencies: 400, Materials: 40, Categories: 8, Invitations: 100, Deliveries: 80, DeliveryItems: 600}, nil
	default:
		return Profile{}, fmt.Errorf("unknown seed profile %q; use full or small", name)
	}
}

type plan struct {
	profile                                               Profile
	at                                                    time.Time
	users, categories, materials, invitations, deliveries []uuid.UUID
	projects                                              []string
	taskCounts, taskEnds                                  []int
	teams                                                 [][]int
	invitees                                              [][]int
}

func newPlan(profile Profile, at time.Time) *plan {
	p := &plan{profile: profile, at: at.UTC().Truncate(time.Microsecond), users: make([]uuid.UUID, profile.Users), categories: make([]uuid.UUID, profile.Categories), materials: make([]uuid.UUID, profile.Materials), invitations: make([]uuid.UUID, profile.Invitations), deliveries: make([]uuid.UUID, profile.Deliveries), taskCounts: make([]int, profile.Projects), taskEnds: make([]int, profile.Projects), teams: make([][]int, profile.Projects)}
	p.invitees = make([][]int, profile.Projects)
	total := 0
	for i := range profile.Projects {
		count := profile.Tasks / profile.Projects
		if profile.Name == "full" {
			switch {
			case i < 2:
				count = 3000
			case i < 20:
				count = 1000
			default:
				count = 240
				if i < 820 {
					count++
				}
			}
		}
		p.taskCounts[i] = count
		total += count
		p.taskEnds[i] = total
		members := 10 + i%41
		if i == 0 {
			members = 50
		}
		for j := range members {
			index := 5 + (i*37+j)%(profile.Users-5)
			if i == 0 {
				index = 5 + j - 2
				if j < 2 {
					index = 2 + j
				}
			}
			p.teams[i] = append(p.teams[i], index)
		}
		membersByIndex := make(map[int]bool, len(p.teams[i]))
		for _, index := range p.teams[i] {
			membersByIndex[index] = true
		}
		candidate := 4 + (i*97)%(profile.Users-4)
		for len(p.invitees[i]) < profile.Invitations/profile.Projects {
			if !membersByIndex[candidate] {
				p.invitees[i] = append(p.invitees[i], candidate)
			}
			candidate = 4 + (candidate-4+1)%(profile.Users-4)
		}
	}
	return p
}
func (p *plan) taskPosition(index int) (int, int) {
	project := sort.Search(len(p.taskEnds), func(i int) bool { return index < p.taskEnds[i] })
	before := 0
	if project > 0 {
		before = p.taskEnds[project-1]
	}
	return project, index - before + 1
}
func (p *plan) stages(project int) (int, int, int) {
	n := p.taskCounts[project]
	done, progress := n*3/5, n*4/5
	if project%10 == 9 {
		done, progress = n, n
	}
	return done / 2, done, progress
}
func (p *plan) taskState(project, number int) string {
	_, done, progress := p.stages(project)
	if number <= done {
		return "done"
	}
	if number <= progress {
		return "in_progress"
	}
	return "not_started"
}
func (p *plan) required(project, number int) []int {
	first, done, progress := p.stages(project)
	primary, secondary := 1+number%2, 3
	switch {
	case number <= first:
	case number <= done:
		primary, secondary = 4+(number-1)%(first-3), 1
	case number <= progress:
		primary, secondary = first+1+(number-1)%(done-first), 1
	case number <= progress+(p.taskCounts[project]-progress)/2:
		primary, secondary = done+1+(number-1)%(progress-done), 1
	default:
		primary, secondary = 4+(number-1)%(first-3), 1
		if number%2 == 0 {
			primary = progress + 1 + (number-1)%((p.taskCounts[project]-progress)/2)
		}
	}
	values := []int{primary, secondary}
	if number <= 9 {
		values = append(values, 3-primary)
	}
	return values
}
func (p *plan) projectStart(project int) time.Time {
	cycles := p.profile.Deliveries / p.profile.Projects / 5
	return p.at.AddDate(0, 0, -28*(cycles-1)-14)
}
func (p *plan) taskDates(project, number int) (string, string) {
	start := p.at.AddDate(0, 0, -30-number%30)
	end := p.at.AddDate(0, 0, -1-number%20)
	switch p.taskState(project, number) {
	case "in_progress":
		end = p.at.AddDate(0, 0, number%30-15)
	case "not_started":
		start = p.at.AddDate(0, 0, number%30-10)
		end = start.AddDate(0, 0, 20)
	}
	if start.Before(p.projectStart(project)) {
		start = p.projectStart(project)
	}
	return start.Format(time.DateOnly), end.Format(time.DateOnly)
}
func taskCode(project string, number int) string { return fmt.Sprintf("%s-T-%04d", project, number) }
func email(index int) string {
	if index < 5 {
		return []string{"superadmin@example.test", "manager@example.test", "admin@example.test", "member@example.test", "outsider@example.test"}[index]
	}
	return fmt.Sprintf("user-%04d@example.test", index)
}
func role(index int) string {
	if index == 0 {
		return "superadmin"
	}
	if index%100 == 1 {
		return "company_store_manager"
	}
	return "user"
}
func (p *plan) invitation(index int) (int, int, string, time.Time, *time.Time) {
	each := p.profile.Invitations / p.profile.Projects
	project, local := index/each, index%each
	status := []string{"pending", "accepted", "rejected", "withdrawn"}[local%4]
	target := p.invitees[project][local]
	if status == "accepted" {
		target = p.teams[project][1+local/4]
	}
	created := p.projectStart(project).Add(12 * time.Hour)
	var completed *time.Time
	if status == "pending" {
		created = p.at.Add(-time.Duration(local+1) * time.Hour)
	} else {
		at := created.Add(time.Hour)
		if status == "accepted" {
			at = p.projectStart(project).Add(24 * time.Hour)
		}
		completed = &at
	}
	return project, target, status, created, completed
}

type movement struct {
	project, cycle, kind, lines int
	direction, status           string
	created                     time.Time
	completed                   *time.Time
	sender, actor               int
}

func (p *plan) movement(index int) movement {
	each := p.profile.Deliveries / p.profile.Projects
	project, local := index/each, index%each
	cycle, kind := local/5, local%5
	cycles := each / 5
	m := movement{project: project, cycle: cycle, kind: kind, lines: 7 + cycle%2, direction: "company_to_project", status: "delivered", sender: 1, actor: p.teams[project][0]}
	if kind == 1 || kind == 4 {
		m.direction = "project_to_company"
		m.sender, m.actor = m.actor, m.sender
	}
	if kind != 0 && kind != 3 {
		m.status = []string{"delivered", "rejected", "cancelled", "in_transit"}[(project+cycle+kind)%4]
		if m.status == "in_transit" && cycle < cycles-1 {
			m.status = "delivered"
		}
	}
	if m.status == "cancelled" {
		m.actor = m.sender
	}
	day := time.Date(p.at.Year(), p.at.Month(), p.at.Day(), 0, 0, 0, 0, time.UTC)
	m.created = day.AddDate(0, 0, -28*(cycles-1-cycle)-5+kind).Add(time.Duration(8+project%12) * time.Hour)
	if m.status != "in_transit" {
		at := m.created.Add(time.Hour)
		m.completed = &at
	}
	return m
}
func (p *plan) line(m movement, index int) (uuid.UUID, pgtype.Numeric) {
	material := index
	if index >= 2 {
		used := p.profile.Materials * 9 / 10
		palette := min(128, used)
		family := m.kind
		if family == 1 {
			family = 0
		}
		if family == 4 {
			family = 3
		}
		slot := (m.cycle*17 + family*31 + (index-2)*11) % (palette - 2)
		material = 2 + (m.project*127+slot*131)%(used-2)
	}
	milli := int64(5+(m.project+m.cycle+index)%21)*1000 + 125
	return p.materials[material], pgtype.Numeric{Int: big.NewInt(milli), Exp: -3, Valid: true}
}
