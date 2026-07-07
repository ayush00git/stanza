import Navbar from '../components/Navbar'
import Hero from '../components/Hero'
import TargetSearch from '../components/TargetSearch'
import Pipeline from '../components/Pipeline'
import Integrations from '../components/Integrations'
import Footer from '../components/Footer'

export default function Home() {
  return (
    <div className="min-h-screen bg-paper">
      <Navbar />
      <main>
        <Hero />
        <TargetSearch />
        <Pipeline />
        <Integrations />
      </main>
      <Footer />
    </div>
  )
}
