# Trips Redirect

![Docker Image Size](https://img.shields.io/badge/Docker%20Image-11.5%20MB-blue)
![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)

A simple and intelligent redirector for your Polarsteps trips. This service allows you to use a custom domain to automatically redirect to your latest Polarsteps trip.

## How it Works

The service works by mapping a domain name to a Polarsteps username. When a request is received, it does the following:

1.  **Finds your username**: It looks up the Polarsteps username associated with the domain name in the `domains.yaml` file.
2.  **Fetches your trips**: It uses the Polarsteps API to get a list of all your trips.
3.  **Selects the best trip**: It intelligently selects which trip to redirect to based on the following priority:
    1.  An ongoing trip.
    2.  The next upcoming trip (closest to the current date).
    3.  The most recently completed trip.
4.  **Redirects**: It redirects the user to the selected trip's URL. If no suitable trip is found, it redirects to your main Polarsteps profile page.

## Setup with Docker

This project is designed to be run with Docker.

### Prerequisites

*   [Docker](https://www.docker.com/get-started) installed on your machine.

### Running the service

1.  **Edit `domains.yaml` file**:
    Edit `domains.yaml` file in the root of the project with the following format:

    ```yaml
    domains:
      your-domain.com: your-polarsteps-username
      another-domain.com: another-polarsteps-username
    ```
The service will automatically handle `www.` subdomains. For example, if you configure `example.com`, `www.example.com` will also be redirected.

2.  **Build and run the Docker container**:

    ```bash
    docker build -t trips-redirect .
    docker run -d -p 3000:3000 -v $(pwd)/domains.yaml:/domains.yaml --name trips-redirect trips-redirect
    ```

    This will start the service on port 3000.

### Environment Variables

The following environment variables can be configured:

*   `PORT` - The port to run the service on (default: 3000)

#### Rybbit Analytics (Optional)

The service supports [Rybbit Analytics](https://rybbit.io) for tracking redirections and errors. To enable analytics, set the following environment variables:

*   `RYBBIT_API_KEY` - Your Rybbit API key
*   `RYBBIT_API_URL` - The Rybbit API endpoint URL
*   `RYBBIT_SITE_ID` - Your Rybbit site ID

All three variables must be set to enable analytics. If any variable is missing, analytics will be disabled.

**Example with analytics enabled:**

```bash
docker run -d -p 3000:3000 \
  -v $(pwd)/domains.yaml:/domains.yaml \
  -e RYBBIT_API_KEY=your-api-key \
  -e RYBBIT_API_URL=https://api.rybbit.io/events \
  -e RYBBIT_SITE_ID=your-site-id \
  --name trips-redirect trips-redirect
```

The service tracks the following events:
- **Pageview**: Successful redirections and profile fallbacks
- **Outbound**: Redirects to Polarsteps trip pages
- **Error**: 404 errors and API failures

## Contributing

Contributions are welcome! If you have any ideas, suggestions, or bug reports, please open an issue or submit a pull request.

Here are some ways you can contribute:

*   Improve the documentation.
*   Add new features.
*   Fix bugs.
*   Suggest new ideas.

## Size of the image

```bash
docker manifest inspect ghcr.io/thomasorgeval/trips-redirect --verbose | jq '.[0].OCIManifest.layers[].size' | awk '{sum += $1} END {print "Total compressed size: " sum " bytes (" sum/1024/1024 " MB)"}'
```
