#import <Cocoa/Cocoa.h>

// hideTitlebar makes the title bar transparent and hides its title. The web
// view then fills the window, and the window keeps its traffic lights.
void hideTitlebar(void *window) {
	NSWindow *w = (__bridge NSWindow *)window;
	w.titlebarAppearsTransparent = YES;
	w.titleVisibility = NSWindowTitleHidden;
	w.styleMask |= NSWindowStyleMaskFullSizeContentView;
}
