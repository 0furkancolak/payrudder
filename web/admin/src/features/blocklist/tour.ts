import { FIND_STEP, pageTour } from "@/shared/ui/tour";

export const blocklistTour = pageTour("blocklist", "shell.items.blocklist", [{ id: "intro", target: "h1" }, FIND_STEP]);
export const platformBlocklistTour = pageTour("platformBlocklist", "shell.items.platformBlocklist", [{ id: "intro", target: "h1" }, FIND_STEP]);
