# Remote Waitlist Manager — implementation

A virtual restaurant waitlist: diners join from their phone, watch their place in line, check in when a table is ready, and are released automatically when their meal time ends.

- **Stack**: Go server with server-rendered HTML (Go templates) and two small vanilla JavaScript islands (no frontend dependencies, not an SPA), MongoDB (single-node replica set for transactions), Playwright end-to-end tests, Docker Compose.
- **Rules implemented**: 10 seats, 3 seconds per person, strict first-come-first-served (a smaller party never jumps ahead of the party at the head of the line), one active registration per email, no overbooking under concurrent requests.
- The original assignment text is kept unchanged below the line.

## Run locally

**Requirements**

- Docker with the Compose plugin (verified with Docker 29.9.2 and Docker Compose v5.6.1)
- Port `8080` free on `127.0.0.1` (or set `APP_PORT`, see below)
- Internet access the first time, to pull the `golang`, `mongo` and distroless images

No local Go, Node or MongoDB installation is needed.

**Start**

```sh
git clone https://github.com/KBJ14/fullstack-swe-takehome.git
cd fullstack-swe-takehome
# if this work is not merged into main yet: git checkout feat/waitlist-manager
docker compose up -d --build --wait
```

The command returns when MongoDB is initialized and the app is running.

**Open**

- Diner app: <http://127.0.0.1:8080/>
- Readiness check: `curl http://127.0.0.1:8080/readyz` → `{"status":"ready"}`

To try several parties at once, use different browsers or private windows: each browser keeps its own registration in a cookie.

**Use a different port**

```sh
APP_PORT=8081 docker compose up -d --build --wait   # then open http://127.0.0.1:8081/
```

**Stop**

```sh
docker compose down      # stops and removes containers; the database volume is kept
docker compose down -v   # also deletes the database volume (all registrations)
```

## More documentation (Korean)

- [PROJECT.md](PROJECT.md) — services, configuration (`.env.example`), screen routes, API examples, and how to run the Go and Playwright tests (`scripts/e2e.sh`, `scripts/e2e-restart.sh`)
- [docs/design.md](docs/design.md) — requirements, policy decisions and interpretations, data model, transactions, API contract, UI (SSR + islands)
- [docs/verification.md](docs/verification.md) — test scenarios and recorded results, including what was verified only in Chromium and what remains unverified

---

# TableCheck SWE Fullstack Take-Home Assignment

Remote Waitlist Manager is a full-stack application designed to handle the waitlist of your restaurant. It manages seating, queueing, and notifications for your diners. **Multiple parties** should be able to join your restaurant's waitlist **concurrently**. Instead of waiting in line to write your name on a piece of paper, you can now join the waitlist virtually and get notified when your table is ready. This will increase your restaurant's efficiency and provide a better experience for your customers.

The user flow is as follows:

- A party of diners go to their favorite restaurant. It's fully booked, but the restaurant gives the option to join a virtual waitlist accessible via browser.
- When the diner opens the app they're asked to input their name and party size.
- After joining the waitlist, they can check the app to verify if it's their turn.
- When the table is ready for them, they check-in via the app and get seated.

## Technical Requirements

Whatever stack you choose, your entire app should be runnable via `docker compose up` locally.

### Frontend

You must use one of these patterns:

1. Isomorphic SSR
2. Pure SSR/no interactivity
3. Islands

SPAs are not allowed.

You must use one of these frameworks/libs/implementations:

- Pure vanilla js with _no_ dependencies.
- React v18+

### Backend

You must use one of:

1. Ruby on Rails
2. nodejs (any framework besides next/nest)
3. golang (any framework)

### Testing

End-to-end tests must be written with Playwright.

### Database

You must use mongo db.

## Business Requirements

**Restaurant Capacity**

Hardcoded to 10 seats.

**Service Time Calculation**

Hardcoded to 3 seconds per person. Example: A party of 4 takes 12 seconds to complete the service.

**Joining the waitlist**

The diner opens the app that shows a single form with these fields:

- Name
- Email
- Party size

Submitting the form should send the api request to join the waitlist.

**Checking in and starting the service**

When the queued party is ready to begin service, the app should display a "check in" button. When clicked:

- The party is removed from the waitlist queue.
- The number of seats available should be decreased by the party size.
- The service countdown starts for that party.

Importantly, the user _must_ be able to view the state of their queued party across multiple browser sessions.

**Queue management**

When a party completes service:

- The system checks the queue for the next party.
- If the seats available are enough for the next party size, the next party’s app shows a new “Check-in” button.
- If not, wait until enough seats are available.

**Duplicate joins**

A party must never appear on the waitlist twice.

**Concurrency**

If two parties try to join the waitlist at the same time the venue must not become overbooked, provide each party with the best possible experience.

## Clarification

No further questions will be answered about the spec or takehome. Use your best engineerig skills, judgement, business acumen and product skills to craft the best user experience possible.

## Submission Guidelines

1. Create a public GitHub repository for your project.
2. Include this README in your repository, with clear instructions for setting up and running the project locally.
3. Include a brief explanation of your architecture decisions in the README or a separate document.

Please grant access to your repo for these following github users

- `daniellizik`

## Evaluation Criteria

Your submission will be evaluated based these criteria in order of importance:

1. Customer Focus: Is the user experience intuitive? Would _you_ use this application if you were a diner? _Please_ play around with your app as if you were a customer prior to submission.
2. Functionality: Does the application work as specified?
3. Code Quality: Is the code well-structured, readable, and maintainable? Add sufficient comments in places where you think it would help other contributors to onboard more quickly to understand your code.
4. Architecture: Are there clear separations of concerns and good design patterns used?
5. QA: Are you confident in the quality of your product? If you had to refactor or add new features, would you be able to do so without breaking the existing functionality? There is no guideline on how many tests you should write, what type of tests you should write, what level of coverage you need to achieve, etc. We leave it to you to decide how to ensure a level of quality that results in your customers trusting your product.

### Good luck!
