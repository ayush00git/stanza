import Verse from './Verse'

type Piece = {
  title: string
  author: string
  lines: string[]
}

const pieces: Piece[] = [
  {
    title: 'Field Notes',
    author: 'M. Okonkwo',
    lines: [
      'The morning keeps no record of itself,',
      'only the light it leaves against the wall.',
    ],
  },
  {
    title: 'Interval',
    author: 'R. Adler',
    lines: [
      'Between two words I meant to say',
      'a whole season came and went unwritten.',
    ],
  },
]

export default function Anthology() {
  return (
    <section id="anthology" className="mx-auto max-w-5xl px-6 py-20 sm:py-24">
      <div className="flex items-baseline justify-between border-b border-hairline pb-5">
        <h2 className="font-display text-3xl font-normal tracking-[-0.01em] text-ink sm:text-4xl">
          From the anthology
        </h2>
        <span className="font-mono text-xs uppercase tracking-[0.2em] text-muted">
          Published with Stanza
        </span>
      </div>

      <div className="mt-12 grid gap-14 sm:grid-cols-2">
        {pieces.map((piece) => (
          <figure key={piece.title}>
            <Verse lines={piece.lines} />
            <figcaption className="mt-5 pl-4 font-mono text-xs uppercase tracking-[0.15em] text-muted">
              {piece.title}
              <span className="text-hairline"> — </span>
              {piece.author}
            </figcaption>
          </figure>
        ))}
      </div>
    </section>
  )
}
