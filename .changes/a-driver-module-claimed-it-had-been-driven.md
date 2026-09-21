# fixed

The Android driver's own module declared itself available to drive while the
registry the engine reads declared it unavailable, and no run has ever driven
an Android application. Nothing read the module, so the claim was harmless and
invisible. A test now compares every driver module against the registry, so the
two cannot disagree.
