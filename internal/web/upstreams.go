package web

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"sms-platform/internal/hero"
	"sms-platform/internal/pricing"
	"sms-platform/internal/smsman"
	"sms-platform/internal/store"
)

type providerQuote struct {
	Service         string
	Country         string
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

type smsmanCatalogCache struct {
	metadataMu      sync.Mutex
	quoteMu         sync.Mutex
	globalMu        sync.Mutex
	metadataExpires time.Time
	countries       []smsman.Item
	applications    []smsman.Item
	quoteExpires    map[int]time.Time
	quotes          map[int]map[int]smsman.Quote
	globalExpires   time.Time
	globalQuotes    map[int]map[int]smsman.Quote
}

type catalogCacheEntry struct {
	expiresAt time.Time
	snapshot  catalogSnapshot
}

type catalogResponseCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]catalogCacheEntry
}

func newSMSManCatalogCache() *smsmanCatalogCache {
	return &smsmanCatalogCache{quoteExpires: map[int]time.Time{}, quotes: map[int]map[int]smsman.Quote{}}
}

func newCatalogResponseCache(ttl time.Duration) *catalogResponseCache {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &catalogResponseCache{ttl: ttl, entries: map[string]catalogCacheEntry{}}
}

func (c *catalogResponseCache) Get(key string) (catalogSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return catalogSnapshot{}, false
	}
	if time.Now().After(entry.expiresAt) {
		return catalogSnapshot{}, false
	}
	return entry.snapshot, true
}

func (c *catalogResponseCache) GetStale(key string) (catalogSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return catalogSnapshot{}, false
	}
	return entry.snapshot, true
}

func (c *catalogResponseCache) Set(key string, snapshot catalogSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = catalogCacheEntry{expiresAt: time.Now().Add(c.ttl), snapshot: snapshot}
}

func (c *smsmanCatalogCache) metadata(ctx context.Context, client *smsman.Client) ([]smsman.Item, []smsman.Item, error) {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	if time.Now().Before(c.metadataExpires) && len(c.countries) > 0 && len(c.applications) > 0 {
		return c.countries, c.applications, nil
	}
	countries, err := client.Countries(ctx)
	if err != nil {
		return nil, nil, err
	}
	applications, err := client.Applications(ctx)
	if err != nil {
		return nil, nil, err
	}
	c.countries = countries
	c.applications = applications
	c.metadataExpires = time.Now().Add(time.Hour)
	return countries, applications, nil
}

func (c *smsmanCatalogCache) countryQuotes(ctx context.Context, client *smsman.Client, countryID int) (map[int]smsman.Quote, error) {
	c.quoteMu.Lock()
	defer c.quoteMu.Unlock()
	if time.Now().Before(c.quoteExpires[countryID]) {
		return c.quotes[countryID], nil
	}
	quotes, err := client.Quotes(ctx, countryID)
	if err != nil {
		return nil, err
	}
	c.quotes[countryID] = quotes
	c.quoteExpires[countryID] = time.Now().Add(30 * time.Second)
	return quotes, nil
}

func (c *smsmanCatalogCache) allQuotes(ctx context.Context, client *smsman.Client) (map[int]map[int]smsman.Quote, error) {
	c.globalMu.Lock()
	defer c.globalMu.Unlock()
	if time.Now().Before(c.globalExpires) && c.globalQuotes != nil {
		return c.globalQuotes, nil
	}
	quotes, err := client.GlobalQuotes(ctx)
	if err != nil {
		return nil, err
	}
	c.globalQuotes = quotes
	c.globalExpires = time.Now().Add(30 * time.Second)
	return quotes, nil
}

func (s *Server) loadCatalog(ctx context.Context, country string) (catalogSnapshot, error) {
	if country == "" {
		return s.loadGlobalCatalog(ctx)
	}
	snapshot := catalogSnapshot{Quotes: map[string]providerQuote{}}
	quoteCandidates := map[string][]providerQuote{}
	livePricing := s.effectivePricing()
	var heroErr error
	if s.C.HeroKey != "" {
		if countries, err := s.Hero.Countries(ctx); err == nil {
			snapshot.Countries = countries
		}
		snapshot.Services, heroErr = s.Hero.Services(ctx, country)
		if heroErr == nil {
			var offers []hero.Offer
			offers, heroErr = s.Hero.Offers(ctx, country)
			if heroErr == nil {
				for _, offer := range offers {
					if offer.Count > 0 {
						candidateCountry := country
						if candidateCountry == "" {
							candidateCountry = offer.Country
						}
						candidate := providerQuote{Service: offer.Service, Country: candidateCountry, Provider: "hero", ProviderCountry: candidateCountry, ProviderService: offer.Service, Cost: offer.Cost, Rate: livePricing.USDCNY, Count: offer.Count}
						quoteCandidates[offer.Service] = append(quoteCandidates[offer.Service], candidate)
					}
				}
			}
		}
	}
	if s.C.SMSManToken == "" {
		if heroErr != nil {
			return catalogSnapshot{}, heroErr
		}
		snapshot.Quotes = cheapestQuoteMap(quoteCandidates, livePricing)
		return snapshot, nil
	}

	countries, applications, err := s.SMSCache.metadata(ctx, s.SMSMan)
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
		snapshot.Quotes = cheapestQuoteMap(quoteCandidates, livePricing)
		return snapshot, nil
	}
	smsQuotes, err := s.SMSCache.countryQuotes(ctx, s.SMSMan, smsCountryID)
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
		candidate := providerQuote{Service: service.Code, Country: country, Provider: "smsman", ProviderCountry: strconv.Itoa(smsCountryID), ProviderService: strconv.Itoa(applicationID), Cost: quote.Price, Rate: livePricing.SMSManCNY, Count: quote.Count}
		quoteCandidates[service.Code] = append(quoteCandidates[service.Code], candidate)
	}
	snapshot.Quotes = cheapestQuoteMap(quoteCandidates, livePricing)
	if heroErr != nil && len(snapshot.Quotes) == 0 {
		return catalogSnapshot{}, heroErr
	}
	return snapshot, nil
}

func (s *Server) loadGlobalCatalog(ctx context.Context) (catalogSnapshot, error) {
	pricing := s.effectivePricing()
	var heroCountries []hero.Country
	var heroServices []hero.Service
	var heroOffers []hero.Offer
	var heroErr error
	var smsCountries, smsApplications []smsman.Item
	var smsQuotes map[int]map[int]smsman.Quote
	var smsErr error
	var providers sync.WaitGroup

	if s.C.HeroKey != "" {
		providers.Add(1)
		go func() {
			defer providers.Done()
			var calls sync.WaitGroup
			errors := make(chan error, 3)
			calls.Add(3)
			go func() {
				defer calls.Done()
				heroCountries, _ = s.Hero.Countries(ctx)
			}()
			go func() {
				defer calls.Done()
				var err error
				heroServices, err = s.Hero.Services(ctx, "")
				if err != nil {
					errors <- err
				}
			}()
			go func() {
				defer calls.Done()
				var err error
				heroOffers, err = s.Hero.Offers(ctx, "")
				if err != nil {
					errors <- err
				}
			}()
			calls.Wait()
			close(errors)
			for err := range errors {
				if heroErr == nil {
					heroErr = err
				}
			}
		}()
	} else {
		heroErr = errors.New("HeroSMS is not configured")
	}
	if s.C.SMSManToken != "" {
		providers.Add(1)
		go func() {
			defer providers.Done()
			var calls sync.WaitGroup
			errors := make(chan error, 2)
			calls.Add(2)
			go func() {
				defer calls.Done()
				var err error
				smsCountries, smsApplications, err = s.SMSCache.metadata(ctx, s.SMSMan)
				if err != nil {
					errors <- err
				}
			}()
			go func() {
				defer calls.Done()
				var err error
				smsQuotes, err = s.SMSCache.allQuotes(ctx, s.SMSMan)
				if err != nil {
					errors <- err
				}
			}()
			calls.Wait()
			close(errors)
			for err := range errors {
				if smsErr == nil {
					smsErr = err
				}
			}
		}()
	} else {
		smsErr = errors.New("SMS-Man is not configured")
	}
	providers.Wait()

	snapshot := catalogSnapshot{Quotes: map[string]providerQuote{}}
	quoteCandidates := map[string][]providerQuote{}
	if heroErr == nil {
		snapshot.Countries, snapshot.Services = heroCountries, heroServices
		for _, offer := range heroOffers {
			if offer.Count <= 0 {
				continue
			}
			candidate := providerQuote{Service: offer.Service, Country: offer.Country, Provider: "hero", ProviderCountry: offer.Country, ProviderService: offer.Service, Cost: offer.Cost, Rate: pricing.USDCNY, Count: offer.Count}
			quoteCandidates[offer.Service] = append(quoteCandidates[offer.Service], candidate)
		}
	} else if smsErr == nil {
		for _, item := range smsCountries {
			snapshot.Countries = append(snapshot.Countries, hero.Country{ID: item.ID, Eng: item.Name, Chn: item.Name})
		}
		for _, item := range smsApplications {
			snapshot.Services = append(snapshot.Services, hero.Service{Code: strconv.Itoa(item.ID), Name: item.Name})
		}
	}
	if smsErr == nil {
		for smsCountryID, quotes := range smsQuotes {
			canonicalCountry := strconv.Itoa(smsCountryID)
			if heroErr == nil {
				name := ""
				for _, item := range smsCountries {
					if item.ID == smsCountryID {
						name = item.Name
						break
					}
				}
				if id := matchingHeroCountryID(snapshot.Countries, name); id > 0 {
					canonicalCountry = strconv.Itoa(id)
				}
			}
			for _, service := range snapshot.Services {
				applicationID := 0
				if heroErr == nil {
					applicationID = matchingItemID(smsApplications, service.Name)
				} else {
					applicationID, _ = strconv.Atoi(service.Code)
				}
				quote, ok := quotes[applicationID]
				if !ok || quote.Count <= 0 {
					continue
				}
				candidate := providerQuote{Service: service.Code, Country: canonicalCountry, Provider: "smsman", ProviderCountry: strconv.Itoa(smsCountryID), ProviderService: strconv.Itoa(applicationID), Cost: quote.Price, Rate: pricing.SMSManCNY, Count: quote.Count}
				quoteCandidates[service.Code] = append(quoteCandidates[service.Code], candidate)
			}
		}
	}
	snapshot.Quotes = balancedQuoteMap(quoteCandidates, pricing)
	if heroErr != nil && smsErr != nil {
		return catalogSnapshot{}, fmt.Errorf("catalog providers unavailable: %v; %v", heroErr, smsErr)
	}
	return snapshot, nil
}

func balancedQuoteMap(candidates map[string][]providerQuote, pricing livePricing) map[string]providerQuote {
	out := map[string]providerQuote{}
	for service, items := range candidates {
		available := make([]providerQuote, 0, len(items))
		for _, item := range items {
			if item.Count > 0 {
				available = append(available, item)
			}
		}
		if len(available) == 0 {
			continue
		}
		sort.SliceStable(available, func(i, j int) bool {
			left := available[i].priceFen(pricing.Markup)
			right := available[j].priceFen(pricing.Markup)
			if left == right {
				return available[i].Count > available[j].Count
			}
			return left < right
		})
		out[service] = available[len(available)/2]
	}
	return out
}

func cheapestQuoteMap(candidates map[string][]providerQuote, pricing livePricing) map[string]providerQuote {
	out := map[string]providerQuote{}
	for service, items := range candidates {
		for _, item := range items {
			if item.Count <= 0 {
				continue
			}
			current, exists := out[service]
			if !exists || item.priceFen(pricing.Markup) < current.priceFen(pricing.Markup) {
				out[service] = item
			}
		}
	}
	return out
}

func matchingHeroCountryID(items []hero.Country, name string) int {
	wanted := normalizedName(name)
	for _, item := range items {
		if normalizedName(item.Eng) == wanted || normalizedName(item.Chn) == wanted {
			return item.ID
		}
	}
	return 0
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
