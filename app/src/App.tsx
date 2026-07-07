import { lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import Home from './pages/Home'
import { SearchProvider } from './lib/searchStore'

// The structure page pulls in Mol* (~3.3 MB), so it's lazy-loaded — the home
// page stays light and Mol* is fetched only when a card is opened.
const ComplexViewerPage = lazy(() => import('./pages/ComplexViewerPage'))

export default function App() {
  return (
    <BrowserRouter>
      {/* Search state lives above the routes so it survives navigating to a
          structure page and back. */}
      <SearchProvider>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route
            path="/structure/:id"
            element={
              <Suspense
                fallback={
                  <div className="flex min-h-screen items-center justify-center bg-paper">
                    <span className="animate-pulse font-mono text-xs uppercase tracking-[0.15em] text-muted">
                      Loading viewer…
                    </span>
                  </div>
                }
              >
                <ComplexViewerPage />
              </Suspense>
            }
          />
        </Routes>
      </SearchProvider>
    </BrowserRouter>
  )
}
