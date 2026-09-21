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
	fmt.Fprintf(&b, "I am writing to apply for the %s position at %s. ", title, company)
	b.WriteString("I am interested in the opportunity and would appreciate being considered for the role.\n\n")
	b.WriteString("Please find my CV attached for your review. ")
	fmt.Fprintf(&b, "I would welcome the opportunity to discuss how my experience can contribute to %s.\n\n", company)
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
