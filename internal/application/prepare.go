package application

import (
	"fmt"
	"strings"

	"linkedin-jobs/internal/config"
	"linkedin-jobs/internal/models"
)

type PreparedApplication struct {
	CVProfile string
	CVPath    string
	Subject   string
	Body      string
}

func Prepare(job *models.JobPosting, settings config.ApplicationSettings, overrideProfile string) (PreparedApplication, error) {
	if job == nil {
		return PreparedApplication{}, fmt.Errorf("nil job")
	}
	profile, err := SelectCVProfile(job, settings, overrideProfile)
	if err != nil {
		return PreparedApplication{}, err
	}

	candidate := strings.TrimSpace(settings.CandidateName)
	subject := "Application - " + strings.TrimSpace(job.Title)
	if candidate != "" {
		subject += " - " + candidate
	}

	company := strings.TrimSpace(job.Company)
	if company == "" {
		company = "your company"
	}
	title := strings.TrimSpace(job.Title)
	if title == "" {
		title = "the advertised position"
	}

	var b strings.Builder
	b.WriteString("Dear Hiring Team,\n\n")
	fmt.Fprintf(&b, "I am interested in applying for the %s position at %s.\n\n", title, company)
	b.WriteString("With more than three years of experience across consumer finance, sales operations, team leadership, business performance reporting, and process improvement, I have worked in target-driven environments where structured execution, stakeholder coordination, and continuous improvement were important.\n\n")
	fmt.Fprintf(&b, "%s\n\n", applicationRelevanceSentence(job))
	b.WriteString("Please find my CV attached for your consideration. ")
	fmt.Fprintf(&b, "I would welcome the opportunity to discuss how my experience could contribute to %s.\n\n", company)
	b.WriteString("Kind regards")
	if candidate != "" {
		b.WriteString(",\n")
		b.WriteString(candidate)
	} else {
		b.WriteString(",")
	}

	out := PreparedApplication{
		Subject: subject,
		Body:    b.String(),
	}
	if profile != nil {
		out.CVProfile = profile.ID
		out.CVPath = profile.Path
	}
	return out, nil
}

func applicationRelevanceSentence(job *models.JobPosting) string {
	text := strings.ToLower(strings.Join([]string{
		job.Title,
		job.ShortDescription,
		job.Description,
		job.Summary,
	}, " "))

	switch {
	case strings.Contains(text, "relationship manager") ||
		strings.Contains(text, "funding") ||
		strings.Contains(text, "banking"):
		return "My background in consumer finance, customer-facing sales, target management, and branch-level execution is particularly relevant to relationship-building and commercial responsibilities in this role."
	case strings.Contains(text, "business development") ||
		strings.Contains(text, "account executive") ||
		strings.Contains(text, "sales") ||
		strings.Contains(text, "commercial") ||
		strings.Contains(text, "partnership"):
		return "My experience leading sales execution, reviewing pipelines, managing performance targets, and improving field processes aligns well with the commercial and relationship-building responsibilities of this role."
	case strings.Contains(text, "operations") ||
		strings.Contains(text, "process") ||
		strings.Contains(text, "project") ||
		strings.Contains(text, "pmo"):
		return "My experience coordinating cross-functional work, monitoring performance, structuring recurring reviews, and improving operational processes aligns well with the execution and coordination needs of this role."
	case strings.Contains(text, "product") ||
		strings.Contains(text, "digital"):
		return "Alongside my commercial background, I have built independent digital projects and strengthened my product-thinking approach around user needs, structured problem solving, and measurable execution."
	default:
		return "I believe this combination of commercial experience, structured problem solving, and hands-on execution would allow me to contribute effectively in this role."
	}
}

func SelectCVProfile(job *models.JobPosting, settings config.ApplicationSettings, overrideProfile string) (*config.CVProfileSettings, error) {
	overrideProfile = strings.TrimSpace(overrideProfile)
	if overrideProfile != "" {
		for i := range settings.CVProfiles {
			if strings.EqualFold(strings.TrimSpace(settings.CVProfiles[i].ID), overrideProfile) {
				return &settings.CVProfiles[i], nil
			}
		}
		return nil, fmt.Errorf("CV profile %q not found in application.cv_profiles", overrideProfile)
	}

	title := strings.ToLower(job.Title)
	description := strings.ToLower(job.Description)
	bestScore := 0
	bestPriority := -1
	var best *config.CVProfileSettings

	for i := range settings.CVProfiles {
		p := &settings.CVProfiles[i]
		score := 0
		for _, raw := range p.Keywords {
			kw := strings.ToLower(strings.TrimSpace(raw))
			if kw == "" {
				continue
			}
			if strings.Contains(title, kw) {
				score += 5
			}
			if strings.Contains(description, kw) {
				score++
			}
		}
		if score > bestScore || (score == bestScore && score > 0 && p.Priority > bestPriority) {
			bestScore = score
			bestPriority = p.Priority
			best = p
		}
	}
	if best != nil {
		return best, nil
	}

	def := strings.TrimSpace(settings.DefaultCVProfile)
	if def != "" {
		for i := range settings.CVProfiles {
			if strings.EqualFold(strings.TrimSpace(settings.CVProfiles[i].ID), def) {
				return &settings.CVProfiles[i], nil
			}
		}
	}
	return nil, nil
}
