package web

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"sms-platform/internal/hero"
	"sms-platform/internal/pricing"
	"sms-platform/internal/smsman"
	"sms-platform/internal/store"
)

type providerQuote struct {
	Service         string
	Provider        string
	ProviderCountry string
	ProviderService string
	Cost            float64
	Rate            float64
	Count           int
}

func (q providerQuote) priceFen(markup float64) int64 {
	return pricing.SaleFen(q.Cost, q.Rate, markup)
}

type catalogSnapshot struct {
	Countries []hero.Country
	Services  []hero.Service
	Quotes    map[string]providerQuote
}

func (s *Server) loadCatalog(ctx context.Context, country string) (catalogSnapshot, error) {
	snapshot := catalogSnapshot{Quotes: map[string]providerQuote{}}
	var heroErr error
	if s.C.HeroKey != "" {
		snapshot.Countries, heroErr = s.Hero.Countries(ctx)
		if heroErr == nil && country != "" {
			snapshot.Services, heroErr = s.Hero.Services(ctx, country)
		}
		if heroErr == nil && country != "" {
			var offers []hero.Offer
			offers, heroErr = s.Hero.Offers(ctx, country)
			if heroErr == nil {
				for _, offer := range offers {
					if offer.Count > 0 {
						snapshot.Quotes[offer.Service] = providerQuote{Service: offer.Service, Provider: "hero", ProviderCountry: country, ProviderService: offer.Service, Cost: offer.Cost, Rate: s.C.USDCNY, Count: offer.Count}
					}
				}
			}
		}
	}
	if s.C.SMSManToken == "" {
		if heroErr != nil {
			return catalogSnapshot{}, heroErr
		}
		return snapshot, nil
	}

	countries, err := s.SMSMan.Countries(ctx)
	if err != nil {
		if s.C.HeroKey != "" && heroErr == nil {
			return snapshot, nil
		}
		return catalogSnapshot{}, err
	}
	if s.C.HeroKey == "" || heroErr != nil {
		snapshot.Countries = nil
		for _, countryItem := range countries {
			snapshot.Countries = append(snapshot.Countries, hero.Country{ID: countryItem.ID, Eng: countryItem.Name, Chn: countryItem.Name})
		}
	}
	if country == "" {
		return snapshot, nil
	}

	applications, err := s.SMSMan.Applications(ctx)
	if err != nil {
		if s.C.HeroKey != "" && heroErr == nil {
			return snapshot, nil
		}
		return catalogSnapshot{}, err
	}
	if s.C.HeroKey == "" || heroErr != nil {
		snapshot.Services = nil
		for _, application := range applications {
			snapshot.Services = append(snapshot.Services, hero.Service{Code: strconv.Itoa(application.ID), Name: application.Name})
		}
	}

	smsCountryID := 0
	if s.C.HeroKey == "" || heroErr != nil {
		smsCountryID, _ = strconv.Atoi(country)
	} else {
		canonicalName := ""
		for _, item := range snapshot.Countries {
			if strconv.Itoa(item.ID) == country {
				canonicalName = item.Eng
				break
			}
		}
		smsCountryID = matchingItemID(countries, canonicalName)
	}
	if smsCountryID == 0 {
		if heroErr != nil {
			return catalogSnapshot{}, heroErr
		}
		return snapshot, nil
	}
	smsQuotes, err := s.SMSMan.Quotes(ctx, smsCountryID)
	if err != nil {
		if s.C.HeroKey != "" && heroErr == nil {
			return snapshot, nil
		}
		return catalogSnapshot{}, err
	}
	for _, service := range snapshot.Services {
		applicationID := 0
		if s.C.HeroKey == "" {
			applicationID, _ = strconv.Atoi(service.Code)
		} else {
			applicationID = matchingItemID(applications, service.Name)
		}
		quote, ok := smsQuotes[applicationID]
		if !ok || quote.Count <= 0 {
			continue
		}
		candidate := providerQuote{Service: service.Code, Provider: "smsman", ProviderCountry: strconv.Itoa(smsCountryID), ProviderService: strconv.Itoa(applicationID), Cost: quote.Price, Rate: s.C.SMSManCNYRate, Count: quote.Count}
		current, exists := snapshot.Quotes[service.Code]
		if !exists || candidate.priceFen(s.C.Markup) < current.priceFen(s.C.Markup) {
			snapshot.Quotes[service.Code] = candidate
		}
	}
	if heroErr != nil && len(snapshot.Quotes) == 0 {
		return catalogSnapshot{}, heroErr
	}
	return snapshot, nil
}

func matchingItemID(items []smsman.Item, name string) int {
	wanted := normalizedName(name)
	for _, item := range items {
		if normalizedName(item.Name) == wanted {
			return item.ID
		}
	}
	return 0
}

func normalizedName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

type providerActivation struct {
	ID, Phone string
	Cost      float64
}

func (s *Server) acquireNumber(ctx context.Context, order store.SMSOrder) (providerActivation, error) {
	switch order.UpstreamProvider {
	case "smsman":
		countryID, countryErr := strconv.Atoi(order.UpstreamCountry)
		applicationID, applicationErr := strconv.Atoi(order.UpstreamService)
		if countryErr != nil || applicationErr != nil || countryID <= 0 || applicationID <= 0 {
			return providerActivation{}, errors.New("invalid SMS-Man route")
		}
		activation, err := s.SMSMan.Acquire(ctx, countryID, applicationID)
		return providerActivation{ID: activation.ID, Phone: activation.Phone, Cost: order.UpstreamCost}, err
	case "hero", "":
		country := order.UpstreamCountry
		if country == "" {
			country = order.Country
		}
		service := order.UpstreamService
		if service == "" {
			service = order.Service
		}
		activation, err := s.Hero.Acquire(ctx, country, service, order.UpstreamCost)
		return providerActivation{ID: activation.ID, Phone: activation.Phone, Cost: activation.Cost}, err
	default:
		return providerActivation{}, fmt.Errorf("unsupported SMS provider %q", order.UpstreamProvider)
	}
}

func (s *Server) providerStatus(ctx context.Context, order store.SMSOrder) (string, string, error) {
	if order.UpstreamProvider == "smsman" {
		code, err := s.SMSMan.SMS(ctx, order.UpstreamID)
		if smsman.IsPending(err) {
			return "waiting", "", nil
		}
		if err != nil {
			return "", "", err
		}
		return "code_received", code, nil
	}
	status, err := s.Hero.Status(ctx, order.UpstreamID)
	if err != nil {
		return "", "", err
	}
	parsedStatus, code := parseHeroStatus(status)
	return parsedStatus, code, nil
}

func (s *Server) cancelUpstream(ctx context.Context, order store.SMSOrder) (bool, error) {
	if order.UpstreamProvider == "smsman" {
		if err := s.SMSMan.Reject(ctx, order.UpstreamID); err != nil {
			return false, err
		}
		return true, nil
	}
	result, err := s.Hero.SetStatus(ctx, order.UpstreamID, "8")
	if err != nil {
		return false, err
	}
	return hero.CancellationSucceeded(result), nil
}
