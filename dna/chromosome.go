package dna

import (
	"math/rand"
	"bitbucket.org/geneticentropy/mendel-go/config"
	"bitbucket.org/geneticentropy/mendel-go/utils"
)


// Chromosome represents 1 chromosome in an individual's genome.
type Chromosome struct {
	LinkageBlocks []*LinkageBlock
	NumMutations uint32		// keep a running total of the mutations. This is both mutations and initial alleles.
}

// This only creates an empty chromosome for gen 0 as part of Meiosis(). Meiosis() creates a populated chromosome.
func ChromosomeFactory(lBsPerChromosome uint32, initialize bool) *Chromosome {
	c := &Chromosome{
		LinkageBlocks: make([]*LinkageBlock, lBsPerChromosome),
	}

	if initialize {			// first generation
		for i := range c.LinkageBlocks { c.LinkageBlocks[i] = LinkageBlockFactory(c)	}
	}

	return c
}


// Makes a semi-deep copy (everything but the mutations) of a chromosome. "Copy" means actually copy, or create a new chromosome with new LBs that point back to the LB history chain.
func (c *Chromosome) Copy(lBsPerChromosome uint32) (newChr *Chromosome) {
	newChr = ChromosomeFactory(lBsPerChromosome, false)
	newChr.NumMutations = c.NumMutations
	for lbIndex := range c.LinkageBlocks {
		//if config.Cfg.Computation.Transfer_linkage_blocks {
			c.LinkageBlocks[lbIndex].Transfer(c, newChr, lbIndex)
		//} else {
		//	newChr.LinkageBlocks[lbIndex] = LinkageBlockFactory(newChr, lb)
		//}
	}
	return
}


// GetNumLinkages returns the number of linkage blocks from each parent (we assume they always have the same number of LBs from each parent)
func (c *Chromosome) GetNumLinkages() uint32 { return uint32(len(c.LinkageBlocks)) }


// Meiosis creates and returns a chromosome as part of reproduction by implementing the crossover model specified in the config file.
// This is 1 form of Copy() for the Chromosome class.
func (dad *Chromosome) Meiosis(mom *Chromosome, lBsPerChromosome uint32, uniformRandom *rand.Rand) (gamete *Chromosome) {
	gamete = Mdl.Crossover(dad, mom, lBsPerChromosome, uniformRandom)

	/* This is what we used to do in individual.OneOffspring(). Keeping it for reference...
	for lb:=uint32(0); lb< dad.GetNumLinkages(); lb++ {
		// randomly choose which grandparents to get the LBs from
		if uniformRandom.Intn(2) == 0 {
			offspr.LinkagesFromDad[lb] = dad.LinkagesFromDad[lb].Copy()
		} else {
			offspr.LinkagesFromDad[lb] = dad.LinkagesFromMom[lb].Copy()
		}

		if uniformRandom.Intn(2) == 0 {
			offspr.LinkagesFromMom[lb] = mom.LinkagesFromDad[lb].Copy()
		} else {
			offspr.LinkagesFromMom[lb] = mom.LinkagesFromMom[lb].Copy()
		}
	}
	*/

	return
}


// The different implementations of LB crossover to another chromosome during meiosis
type CrossoverType func(dad *Chromosome, mom *Chromosome, lBsPerChromosome uint32, uniformRandom *rand.Rand) *Chromosome

// Create the gamete from all of dad's chromosomes or all of mom's chromosomes.
func NoCrossover(dad *Chromosome, mom *Chromosome, lBsPerChromosome uint32, uniformRandom *rand.Rand) (gamete *Chromosome) {
	//gamete = ChromosomeFactory(lBsPerChromosome, false)
	// Copy all of the LBs from the one or the other
	if uniformRandom.Intn(2) == 0 {
		gamete = dad.Copy(lBsPerChromosome)
	} else {
		gamete = mom.Copy(lBsPerChromosome)
	}
	return
}


// Create the gamete from dad and mom's chromosomes by randomly choosing each LB from either.
func FullCrossover(dad *Chromosome, mom *Chromosome, lBsPerChromosome uint32, uniformRandom *rand.Rand) (gamete *Chromosome) {
	gamete = ChromosomeFactory(lBsPerChromosome, false)
	// Each LB can come from either dad or mom
	for lbIndex :=0; lbIndex <int(dad.GetNumLinkages()); lbIndex++ {
		if uniformRandom.Intn(2) == 0 {
			//if config.Cfg.Computation.Transfer_linkage_blocks {
				lb := dad.LinkageBlocks[lbIndex].Transfer(dad, gamete, lbIndex)
				gamete.NumMutations += lb.GetNumMutations()
			//} else {
			//	lb := LinkageBlockFactory(gamete, dad.LinkageBlocks[lbIndex])
			//	gamete.LinkageBlocks[lbIndex] = lb
			//	gamete.NumMutations += lb.GetNumMutations()
			//}
		} else {
			//if config.Cfg.Computation.Transfer_linkage_blocks {
				lb := mom.LinkageBlocks[lbIndex].Transfer(mom, gamete, lbIndex)
				gamete.NumMutations += lb.GetNumMutations()
			//} else {
			//	lb := LinkageBlockFactory(gamete, mom.LinkageBlocks[lbIndex])
			//	gamete.LinkageBlocks[lbIndex] = lb
			//	gamete.NumMutations += lb.GetNumMutations()
			//}
		}
	}
	return
}


// Create the gamete from dad and mom's chromosomes by randomly choosing sections of LBs from either.
func PartialCrossover(dad *Chromosome, mom *Chromosome, lBsPerChromosome uint32, uniformRandom *rand.Rand) (gamete *Chromosome) {
	gamete = ChromosomeFactory(lBsPerChromosome, false)

	// Algorithm: choose random sizes for <numCrossovers> LB sections for primary and <numCrossovers> LB sections for secondary

	// Choose if dad or mom is the primary chromosome
	var primary, secondary *Chromosome
	if uniformRandom.Intn(2) == 0 {
		primary = dad
		secondary = mom
	} else {
		primary = mom
		secondary = dad
	}

	// Mean_num_crossovers is the average number of crossovers for the chromosome PAIR during Meiosis 1 Metaphase. So for each chromosome (chromotid)
	// the mean = (Mean_num_crossovers / 2). When determining the actual num crossovers for this instance, we get a random number in the
	// range: 0 - (2 * mean + 1) which is (2 * Mean_num_crossovers / 2 + 1) which is (Mean_num_crossovers + 1)
	// To clarify, numCrossovers is the num crossovers in this specific 1 chromosome
	numCrossovers := uniformRandom.Intn(int(config.Cfg.Population.Mean_num_crossovers) + 1)
	//todo: track mean of numCrossovers
	// For numCrossovers=2 the chromosome would normally look like this :  |  S  |         P         |  S  |
	// But to make the section sizes of the secondary and primary more similar we will model it like this :  |  P  |  S  |  P  |  S  |
	// numCrossovers=1 means 2 LB sections, numCrossovers=2 means 3 LB sections, numCrossovers=3 means 5 LB sections
	var numLbSections int
	switch {
	case numCrossovers <= 0:
		// Handle special case of no crossover - copy all LBs from primary
		//config.Verbose(9, " Copying all LBs from primary")
		gamete = primary.Copy(lBsPerChromosome)
		return
	default:
		numLbSections = (2 * numCrossovers)
	}
	//primaryMeanSectionSize := utils.RoundIntDiv(float64(lBsPerChromosome)*(1.0-config.Cfg.Population.Crossover_fraction), float64(numCrossovers))	// weight the primary section size to be bigger
	//secondaryMeanSectionSize := utils.RoundIntDiv(float64(lBsPerChromosome)*config.Cfg.Population.Crossover_fraction, float64(numCrossovers))
	meanSectionSize := utils.RoundIntDiv(float64(lBsPerChromosome), float64(numLbSections))
	//config.Verbose(9, " Mean_num_crossovers=%v, numCrossovers=%v, numLbSections=%v, meanSectionSize=%v\n", config.Cfg.Population.Mean_num_crossovers, numCrossovers, numLbSections, meanSectionSize)

	// Copy each LB section.
	begIndex := 0		// points to the beginning of the next LB section
	maxIndex := int(lBsPerChromosome) - 1	// 0 based
	parent := primary		// we will alternate between secondary and primary
	// go 2 at a time thru the sections, 1 for primary, 1 for secondary
	for section:=1; section<=numLbSections; section++ {
		// Copy LB section
		if begIndex > maxIndex { break }
		var sectionLen int
		if meanSectionSize <= 0 {
			sectionLen = 1		// because we can not pass 0 into Intn()
		} else {
			sectionLen = uniformRandom.Intn(2 * meanSectionSize) + 1		// randomly choose a length for this section that on average will be meanSectionSize. Should never be 0
		}
		endIndex := utils.MinInt(begIndex+sectionLen-1, maxIndex)
		if section >=  numLbSections { endIndex = maxIndex }		// make the last section reach to the end of the chromosome
		//config.Verbose(9, " Copying LBs %v-%v from %v\n", begIndex, endIndex, parent==primary)
		for lbIndex :=begIndex; lbIndex <=endIndex; lbIndex++ {
			//if config.Cfg.Computation.Transfer_linkage_blocks {
				lb := parent.LinkageBlocks[lbIndex].Transfer(parent, gamete, lbIndex)
				gamete.NumMutations += lb.GetNumMutations()
			//} else {
			//	lb := LinkageBlockFactory(gamete, parent.LinkageBlocks[lbIndex])
			//	gamete.LinkageBlocks[lbIndex] = lb
			//	gamete.NumMutations += lb.GetNumMutations()
			//}
		}

		// For next iteration
		begIndex = endIndex + 1
		if parent == primary {
			parent = secondary
		} else {
			parent = primary
		}
	}
	return
}


// AppendMutation creates and adds a mutations to the LB specified
func (c *Chromosome) AppendMutation(lbInChr int, mutId uint64, uniformRandom *rand.Rand) {
	// Note: to try to save time, we could accumulate the chromosome fitness as we go, but doing so would bypass the LB method
	//		of calculating its own fitness, so we won't do that.
	c.LinkageBlocks[lbInChr].AppendMutation(mutId, uniformRandom)
	c.NumMutations++
}

// SumFitness combines the fitness effect of all of its LBs in the additive method
func (c *Chromosome) SumFitness() (fitness float64) {
	for _, lb := range c.LinkageBlocks {
		fitness += lb.SumFitness()
	}
	// Note: we don't bother caching the fitness in the chromosome, because we cache the total in the individual, and we know better when to cache there.
	return
}


// GetMutationStats returns the number of deleterious, neutral, favorable mutations, and the average fitness factor of each
func (c *Chromosome) GetMutationStats() (deleterious, neutral, favorable uint32, avDelFit, avFavFit float64) {
	// Calc the average of each type of mutation: multiply the average from each LB and num mutns from each LB, then at the end divide by total num mutns
	for _,lb := range c.LinkageBlocks {
		delet, neut, fav, avD, avF := lb.GetMutationStats()
		deleterious += delet
		neutral += neut
		favorable += fav
		avDelFit += (float64(delet) * avD)
		avFavFit += (float64(fav) * avF)
	}
	if deleterious > 0 { avDelFit = avDelFit / float64(deleterious) }
	if favorable > 0 { avFavFit = avFavFit / float64(favorable) }
	// Note: we don't bother caching the fitness stats in the chromosome, because we cache the total in the individual, and we know better when to cache there.
	return
}


// GetInitialAlleleStats returns the number of deleterious, neutral, favorable initial alleles, and the average fitness factor of each
func (c *Chromosome) GetInitialAlleleStats() (deleterious, neutral, favorable uint32, avDelFit, avFavFit float64) {
	// Calc the average of each type of allele: multiply the average from each LB and num alleles from each LB, then at the end divide by total num alleles
	for _,lb := range c.LinkageBlocks {
		delet, neut, fav, avD, avF := lb.GetInitialAlleleStats()
		deleterious += delet
		neutral += neut
		favorable += fav
		avDelFit += (float64(delet) * avD)
		avFavFit += (float64(fav) * avF)
	}
	if deleterious > 0 { avDelFit = avDelFit / float64(deleterious) }
	if favorable > 0 { avFavFit = avFavFit / float64(favorable) }
	// Note: we don't bother caching the fitness stats in the chromosome, because we cache the total in the individual, and we know better when to cache there.
	return
}


/*
// GatherAlleles counts all of this chromosome's alleles (both mutations and initial alleles) and adds them to the given struct
func (c *Chromosome) GatherAlleles(alleles *Alleles) {
	for _, lb := range c.LinkageBlocks { lb.GatherAlleles(alleles) }
}
*/


// CountAlleles adds all of this chromosome's alleles (both mutations and initial alleles) to the given struct
func (c *Chromosome) CountAlleles(allelesForThisIndiv *AlleleCount) {
	for _, lb := range c.LinkageBlocks { lb.CountAlleles(allelesForThisIndiv) }
}
