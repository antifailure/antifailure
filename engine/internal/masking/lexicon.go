package masking

// The lexicons a transform draws from.
//
// Two rules govern what is in them. Every entry is obviously synthetic on
// inspection, so that nobody reading a masked database wonders whether a row
// is real. And the domains are reserved by RFC 6761 and RFC 2606, so a masked
// address can never resolve, can never receive mail, and can never be
// accidentally emailed by an application under test.

// syntheticDomain is reserved by RFC 6761 and can never be registered or
// resolved. Every masked email address ends here, which is the last line of
// defence if an application under test somehow reaches a real mail provider.
const syntheticDomain = "example.test"

// givenNames and familyNames produce recognisably fake but plausibly shaped
// names, so that a user interface built for real names still looks right.
var givenNames = []string{
	"Ada", "Bess", "Cyra", "Dana", "Edda", "Fern", "Gwen", "Hana", "Iris", "Juno",
	"Kira", "Lena", "Mira", "Nola", "Orla", "Pila", "Quin", "Rhea", "Sena", "Tara",
	"Uma", "Vera", "Wren", "Xena", "Yara", "Zola", "Arlo", "Bram", "Colm", "Dane",
	"Emre", "Finn", "Gale", "Hugo", "Ivo", "Jory", "Kian", "Loki", "Mace", "Niko",
	"Odin", "Piet", "Quil", "Remy", "Soren", "Theo", "Ulf", "Vlad", "Wade", "Yuri",
}

var familyNames = []string{
	"Aldwin", "Brackle", "Cordell", "Dunmore", "Everly", "Fenwick", "Grimsby",
	"Halloway", "Ingram", "Jarrow", "Kestrel", "Larkspur", "Mardale", "Norwood",
	"Osgood", "Prescott", "Quarrow", "Ridley", "Stanmore", "Thorne", "Underhill",
	"Vandermere", "Wexford", "Yarrow", "Ziegler", "Ashcombe", "Blythewood",
	"Carrowmore", "Draycott", "Ellesmere", "Fairholm", "Glenbrook", "Hartwell",
	"Ivyridge", "Jessup", "Kingsley", "Lindenow", "Marchmont", "Newbold", "Oakhurst",
}

// companyWords build company names that read as companies without resembling
// any that exist.
var companyPrefixes = []string{
	"Northwind", "Bluecrest", "Ironvale", "Silverpine", "Redwater", "Goldmarsh",
	"Stonebridge", "Fairhaven", "Greyloch", "Amberfield", "Westbrook", "Highmoor",
	"Copperline", "Larkfield", "Marchvale", "Oakstead", "Pinehollow", "Quarryside",
}

var companySuffixes = []string{
	"Systems", "Logistics", "Analytics", "Holdings", "Partners", "Industries",
	"Collective", "Works", "Labs", "Group", "Supply", "Networks", "Foundry", "Union",
}

// streetWords build addresses in a shape that passes a form validator.
var streetNames = []string{
	"Alder", "Birch", "Cedar", "Dogwood", "Elm", "Fircone", "Gorse", "Hazel",
	"Ivywall", "Juniper", "Kingfisher", "Linden", "Maple", "Nettle", "Oakley",
	"Poplar", "Quince", "Rowan", "Sycamore", "Thistle", "Umber", "Vervain",
	"Willow", "Yewtree",
}

var streetTypes = []string{
	"Street", "Avenue", "Road", "Lane", "Way", "Close", "Court", "Terrace", "Row",
}

var cityNames = []string{
	"Ashford", "Bellcrest", "Cranwell", "Dellmoor", "Eastmere", "Fallowick",
	"Glasholm", "Hartness", "Inglewood", "Jarrowfield", "Kestrelby", "Lowmarsh",
	"Millbrook", "Northgate", "Orchardvale", "Pentworth", "Quayside", "Rushmere",
	"Southfell", "Thornbury", "Upminster", "Valebridge", "Westhaven", "Yarnton",
}

// regionCodes are the two letter subdivision codes an address form expects.
var regionCodes = []string{
	"AA", "AB", "AC", "AD", "AE", "AF", "AG", "AH", "AJ", "AK",
	"AL", "AM", "AN", "AP", "AQ", "AR", "AS", "AT", "AU", "AV",
}

// sentenceWords build free text of a realistic shape, so that a column holding
// a review or a note still exercises the layout it was written for.
var sentenceWords = []string{
	"the", "process", "handled", "our", "request", "quickly", "and", "the",
	"result", "matched", "what", "was", "described", "in", "advance", "which",
	"made", "planning", "straightforward", "for", "everyone", "involved", "over",
	"several", "weeks", "of", "steady", "work", "without", "any", "surprises",
	"that", "would", "have", "needed", "a", "second", "conversation", "later",
}

// base32Alphabet is Crockford's, which omits I, L, O, and U so that a masked
// identifier read aloud or typed by hand is unambiguous.
const base32Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
