package names

import (
	"math/rand/v2"
	"strings"
)

// ~1000 short, memorable, easy-to-type English words.
// Curated for: no ambiguity, no offensive words, typeable in <6 keystrokes.
var words = []string{
	// Nature
	"ash", "aspen", "bay", "birch", "bloom", "bolt", "brook", "cairn",
	"cedar", "cliff", "cloud", "coral", "cove", "creek", "crest", "dawn",
	"delta", "dew", "drift", "dune", "dusk", "elm", "fern", "field",
	"fjord", "flint", "flora", "fog", "forge", "frost", "gale", "glen",
	"grove", "haze", "heath", "hill", "holly", "iris", "ivy", "lake",
	"larch", "leaf", "lily", "lotus", "marsh", "mesa", "mist", "moon",
	"moss", "oak", "oasis", "olive", "orchid", "palm", "peak", "pine",
	"plum", "pond", "rain", "reed", "ridge", "river", "root", "sage",
	"shade", "shoal", "shore", "slate", "snow", "solar", "stone", "storm",
	"sun", "thorn", "tide", "trail", "tulip", "vale", "vine", "wave",
	"weed", "wheat", "wild", "wind", "wood", "yew",

	// Animals
	"bear", "crow", "crane", "deer", "dove", "drake", "eagle", "elk",
	"finch", "fox", "goat", "hawk", "heron", "horse", "ibis", "jay",
	"kite", "lark", "lion", "lynx", "moth", "newt", "orca", "osprey",
	"otter", "owl", "panda", "pike", "quail", "ram", "raven", "robin",
	"seal", "shark", "snake", "stork", "swan", "swift", "tiger", "toad",
	"trout", "viper", "wren", "wolf",

	// Materials & elements
	"amber", "brass", "brick", "chrome", "clay", "coal", "cobalt",
	"copper", "crystal", "diamond", "dust", "ember", "garnet", "glass",
	"gold", "granite", "iron", "ivory", "jade", "jet", "lead", "linen",
	"marble", "mercury", "nickel", "obsidian", "onyx", "opal", "pearl",
	"quartz", "ruby", "rust", "satin", "silk", "silver", "steel",
	"tin", "titanium", "topaz", "velvet", "zinc",

	// Objects & tools
	"anchor", "anvil", "arch", "arrow", "atlas", "axe", "badge",
	"banner", "barrel", "beacon", "bell", "blade", "bolt", "bridge",
	"candle", "castle", "chain", "charm", "chest", "cipher", "clock",
	"coil", "coin", "compass", "crown", "dagger", "dial", "drum",
	"edge", "engine", "flag", "flame", "flask", "flute", "gear",
	"glyph", "grid", "hammer", "harp", "helm", "hinge", "hook",
	"hub", "key", "knot", "lamp", "lance", "lantern", "latch",
	"lens", "lever", "lock", "mast", "mill", "mirror", "nail",
	"needle", "nest", "node", "orbit", "page", "panel", "patch",
	"peg", "pen", "pixel", "plank", "plate", "pledge", "plug",
	"pole", "portal", "prism", "probe", "pulse", "quill", "rack",
	"rail", "ramp", "ring", "rod", "rope", "rune", "scale",
	"scroll", "seed", "shard", "shell", "shield", "signal", "siren",
	"slab", "slot", "spoke", "spool", "spring", "staff", "stamp",
	"strut", "stud", "switch", "sword", "token", "torch", "tower",
	"trap", "valve", "vault", "wedge", "wheel", "wire",

	// Abstract & misc
	"aura", "axis", "blaze", "bliss", "brisk", "calm", "chaos",
	"clash", "crisp", "cross", "cycle", "dash", "depth", "echo",
	"fable", "faith", "feast", "flash", "flare", "flex", "flux",
	"focus", "force", "frame", "gleam", "glow", "grace", "grasp",
	"grit", "haven", "heart", "helix", "honor", "hope", "index",
	"karma", "light", "logic", "march", "mark", "might", "myth",
	"nerve", "noble", "north", "nova", "omega", "onset", "order",
	"pace", "phase", "pivot", "plan", "ploy", "plume", "point",
	"power", "prime", "proof", "quest", "rapid", "reach", "reign",
	"ripple", "route", "rush", "scope", "scout", "sense", "shift",
	"sight", "sigma", "skill", "slate", "slope", "solar", "sonic",
	"south", "space", "span", "spark", "spell", "spire", "spirit",
	"spoke", "squad", "stage", "stake", "stand", "stark", "start",
	"steam", "step", "sting", "stock", "surge", "sweep", "tact",
	"tempo", "theta", "trace", "trait", "trend", "trust", "truth",
	"twist", "unity", "valor", "verge", "vigor", "vital", "vivid",
	"voice", "vortex", "vow", "wake", "warp", "watch", "zenith",
	"zero", "zone",

	// Food & drink
	"basil", "berry", "brew", "candy", "chai", "chili", "cocoa",
	"fig", "ginger", "grape", "honey", "jam", "lemon", "lime",
	"mango", "maple", "melon", "mint", "mocha", "nectar", "nutmeg",
	"peach", "pepper", "plum", "raisin", "spice", "sugar", "syrup",
	"thyme", "toast", "toffee", "vanilla", "walnut",

	// Colors & light
	"azure", "blush", "bronze", "coral", "crimson", "cyan", "ebony",
	"hazel", "indigo", "ivory", "khaki", "lilac", "magenta", "mauve",
	"navy", "ochre", "rose", "scarlet", "teal", "umber", "violet",

	// Music & sound
	"alto", "bass", "beat", "chord", "forte", "hymn", "lyric",
	"note", "piano", "reed", "rhythm", "riff", "song", "tempo",
	"tenor", "tone", "treble", "tune", "verse", "vocal",

	// Time & space
	"apex", "comet", "cosmic", "dusk", "epoch", "equinox", "lunar",
	"nebula", "noon", "orbit", "phase", "polar", "pulsar", "solar",
	"star", "stellar", "summit", "sunset", "zenith",

	// Geography
	"atlas", "bay", "cape", "canyon", "coast", "delta", "harbor",
	"island", "mesa", "oasis", "ravine", "reef", "ridge", "shore",
	"strait", "summit", "tundra", "valley",
}

const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// Generate returns a name like "wolf-a3f2" - word + 4-char base36 suffix.
// ~1000 words × 36^4 suffixes = ~1.7 billion unique names.
func Generate() string {
	word := words[rand.IntN(len(words))]
	var sb strings.Builder
	sb.WriteString(word)
	sb.WriteByte('-')
	for range 4 {
		sb.WriteByte(base36[rand.IntN(36)])
	}
	return sb.String()
}

// Prefix returns the word part before the suffix (e.g. "wolf" from "wolf-a3f2").
func Prefix(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '-' {
			return name[:i]
		}
	}
	return name
}
