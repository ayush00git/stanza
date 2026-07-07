import Navbar from '../components/Navbar'
import Hero from '../components/Hero'
import Features from '../components/Features'
import Anthology from '../components/Anthology'
import Footer from '../components/Footer'

export default function Home() {
  return (
    <div className="min-h-screen bg-paper">
      <Navbar />
      <main>
        <Hero />
        <Features />
        <Anthology />
      </main>
      <Footer />
    </div>
  )
}
