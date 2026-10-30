package template

// The word lists behind the random helper. They are short on purpose: the helper makes plausible
// values for a mock, not a dataset.

func firstNames() []string {
	return []string{
		"Alice", "Amir", "Ana", "Ben", "Carlos", "Chen", "Chloe", "Daniel", "Elena", "Emma", "Farah", "Grace",
		"Hana", "Hugo", "Isabel", "Ivan", "Jack", "Julia", "Kofi", "Layla", "Liam", "Maria", "Mohammed", "Nina",
		"Noah", "Olivia", "Omar", "Priya", "Rosa", "Samuel", "Sofia", "Tariq", "Uma", "Victor", "Wei", "Yara", "Zoe",
	}
}

func lastNames() []string {
	return []string{
		"Adams", "Baker", "Chen", "Clarke", "Davies", "Evans", "Fischer", "Garcia", "Hughes", "Ibrahim", "Jones",
		"Kim", "Lopez", "Martin", "Nguyen", "Okafor", "Patel", "Quinn", "Rossi", "Silva", "Taylor", "Usman", "Vega",
		"Walker", "Xu", "Young", "Zhang",
	}
}

func streetSuffixes() []string {
	return []string{"Street", "Avenue", "Road", "Lane", "Drive", "Court", "Way", "Place", "Boulevard"}
}

func cities() []string {
	return []string{
		"Springfield", "Riverton", "Fairview", "Georgetown", "Clinton", "Franklin", "Madison", "Salem", "Ashland",
		"Bristol", "Dover", "Oxford", "Lakeside", "Hillcrest", "Kingston", "Newport", "Portland", "Waverly",
	}
}

func states() []string {
	return []string{
		"Alabama", "Alaska", "Arizona", "California", "Colorado", "Florida", "Georgia", "Hawaii", "Illinois", "Maine",
		"Nevada", "New York", "Ohio", "Oregon", "Texas", "Utah", "Vermont", "Washington",
	}
}

func stateAbbreviations() []string {
	return []string{"AL", "AK", "AZ", "CA", "CO", "FL", "GA", "HI", "IL", "ME", "NV", "NY", "OH", "OR", "TX", "UT", "VT", "WA"}
}

func countries() []string {
	return []string{
		"Australia", "Brazil", "Canada", "Chile", "Egypt", "France", "Germany", "India", "Japan", "Kenya", "Mexico",
		"New Zealand", "Norway", "Peru", "Poland", "Spain", "Sweden", "United Kingdom", "United States",
	}
}

func countryCodes() []string {
	return []string{"AU", "BR", "CA", "CL", "EG", "FR", "DE", "IN", "JP", "KE", "MX", "NZ", "NO", "PE", "PL", "ES", "SE", "GB", "US"}
}

func topDomains() []string {
	return []string{"com", "org", "net", "io", "co"}
}

func companySuffixes() []string {
	return []string{"Inc", "LLC", "Ltd", "Group", "Partners", "Holdings", "and Sons", "Co"}
}

func industries() []string {
	return []string{"Banking", "Construction", "Education", "Energy", "Healthcare", "Insurance", "Logistics", "Media", "Retail", "Software", "Telecommunications"}
}

func professions() []string {
	return []string{"Accountant", "Architect", "Chef", "Dentist", "Designer", "Electrician", "Engineer", "Journalist", "Nurse", "Pilot", "Teacher", "Translator"}
}

func buzzwords() []string {
	return []string{"agile", "seamless", "scalable", "robust", "synergistic", "dynamic", "intuitive", "proactive", "responsive", "integrated", "flexible", "innovative"}
}

func colors() []string {
	return []string{"red", "orange", "yellow", "green", "blue", "indigo", "violet", "black", "white", "grey", "brown", "pink"}
}

func loremWords() []string {
	return []string{
		"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit", "sed", "do", "eiusmod", "tempor",
		"incididunt", "ut", "labore", "et", "dolore", "magna", "aliqua", "enim", "ad", "minim", "veniam", "quis", "nostrud",
		"exercitation", "ullamco", "laboris", "nisi", "aliquip", "ex", "ea", "commodo", "consequat", "duis", "aute", "irure",
		"in", "reprehenderit", "voluptate", "velit", "esse", "cillum", "fugiat", "nulla", "pariatur", "excepteur", "sint",
		"occaecat", "cupidatat", "non", "proident", "sunt", "culpa", "qui", "officia", "deserunt", "mollit", "anim", "id", "est", "laborum",
	}
}
