package publicapi

// AboutText is the About page's copy as plain text: the shape
// internal/seo.About builds from the shared content source and the
// get_about operation reads. It lives here, not in internal/seo, because
// internal/seo imports internal/handlers, so handlers cannot import seo;
// main.go hands seo.About to the handlers as a function.
type AboutText struct {
	Title    string
	Intro    []string
	Sections []AboutSection
	Links    map[string]string
}

// AboutSection is one headed section of AboutText.
type AboutSection struct {
	Heading, Text string
}
